// Package shell parses a command line with mvdan.cc/sh (zsh dialect) into the
// simple commands it runs, with aliases resolved and wrappers (sudo, env, xargs,
// timeout, ...) unwrapped. Risk rules, the validator and the dry run work on this.
package shell

import (
	"fmt"
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Cmd is one simple command (a CallExpr), after wrappers are unwrapped.
type Cmd struct {
	Name     string         // command name, e.g. "rm" (basename if it was a path)
	Path     string         // as written, if it contained a slash
	Args     []string       // arguments; non-literal ones are "" with Lit[i]=false
	Lit      []bool         // whether Args[i] is a plain literal
	Words    []*syntax.Word // the argument words (for expansion in the dry run)
	Wrappers []string       // e.g. ["sudo", "xargs"]
	Alias    string         // alias the name came from (e.g. "gst")
	Redirs   []Redir        // redirections on this command
	Subst    bool           // an argument contains $(...), `...` or <(...)
	PipeFrom *syntax.Stmt   // left side of the pipe feeding this command (for xargs dry runs)
	Stmt     *syntax.Stmt   // the statement this command belongs to
	Remote   string         // host, if this command runs through ssh
	Assigns  []string       // VAR=value prefixes (names)
}

// Redir is a redirection such as "> out.txt".
type Redir struct {
	Op     string
	Target string // literal target ("" if not literal)
	Lit    bool
}

// Script is a parsed command line.
type Script struct {
	Src  string
	File *syntax.File
	Cmds []*Cmd
	// Structure that makes a dry run unreliable.
	HasLoop, HasFunc, HasSubst bool
}

// Aliases maps alias name -> expansion (from `alias -L` in the user's shell).
type Aliases map[string]string

var wrappersNoArgs = map[string]bool{
	"nohup": true, "command": true, "builtin": true, "exec": true, "noglob": true,
	"nocorrect": true, "time": true, "chronic": true, "unbuffer": true,
}

// wrapper -> flags that take a value
var wrappersWithFlags = map[string]map[string]bool{
	"sudo":       {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-T": true},
	"doas":       {"-u": true, "-C": true},
	"env":        {"-u": true, "-C": true, "-S": true, "-P": true},
	"nice":       {"-n": true},
	"ionice":     {"-c": true, "-n": true},
	"stdbuf":     {"-i": true, "-o": true, "-e": true},
	"caffeinate": {"-t": true, "-w": true},
	"timeout":    {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"xargs":      {"-I": true, "-n": true, "-P": true, "-L": true, "-s": true, "-d": true, "-E": true, "-J": true, "-R": true, "-a": true},
	"watch":      {"-n": true, "-d": false, "--interval": true},
}

// Parse parses src as zsh and returns its simple commands.
func Parse(src string, aliases Aliases) (*Script, error) {
	p := syntax.NewParser(syntax.Variant(syntax.LangZsh), syntax.KeepComments(false))
	f, err := p.Parse(strings.NewReader(src), "")
	if err != nil {
		// zsh dialect support in mvdan is newer; fall back to bash for odd constructs
		p2 := syntax.NewParser(syntax.Variant(syntax.LangBash))
		f2, err2 := p2.Parse(strings.NewReader(src), "")
		if err2 != nil {
			return nil, err
		}
		f = f2
	}
	s := &Script{Src: src, File: f}
	w := walker{s: s, aliases: aliases}
	for _, st := range f.Stmts {
		w.stmt(st, nil, 0)
	}
	return s, nil
}

type walker struct {
	s       *Script
	aliases Aliases
}

func (w *walker) stmt(st *syntax.Stmt, pipeFrom *syntax.Stmt, depth int) {
	if st == nil || depth > 8 {
		return
	}
	redirs := convRedirs(st.Redirs)
	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		w.call(st, c, redirs, pipeFrom, depth)
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.stmt(c.X, pipeFrom, depth+1)
			w.stmt(c.Y, c.X, depth+1)
		} else {
			w.stmt(c.X, nil, depth+1)
			w.stmt(c.Y, nil, depth+1)
		}
	case *syntax.Block:
		w.stmts(c.Stmts, depth)
	case *syntax.Subshell:
		w.stmts(c.Stmts, depth)
	case *syntax.IfClause:
		for ic := c; ic != nil; ic = ic.Else {
			w.stmts(ic.Cond, depth)
			w.stmts(ic.Then, depth)
		}
	case *syntax.WhileClause:
		w.s.HasLoop = true
		w.stmts(c.Cond, depth)
		w.stmts(c.Do, depth)
	case *syntax.ForClause:
		w.s.HasLoop = true
		w.words(forWords(c), depth)
		w.stmts(c.Do, depth)
	case *syntax.CaseClause:
		for _, it := range c.Items {
			w.stmts(it.Stmts, depth)
		}
	case *syntax.FuncDecl:
		w.s.HasFunc = true
		w.stmt(c.Body, nil, depth+1)
	case *syntax.TimeClause:
		w.stmt(c.Stmt, pipeFrom, depth+1)
	case *syntax.CoprocClause:
		w.stmt(c.Stmt, nil, depth+1)
	case *syntax.DeclClause:
		// export/local/typeset: harmless, but look inside values for $(...)
		for _, a := range c.Args {
			if a.Value != nil {
				w.words([]*syntax.Word{a.Value}, depth)
			}
		}
	}
	// output redirections on compound commands still matter
	if _, ok := st.Cmd.(*syntax.CallExpr); !ok && len(redirs) > 0 {
		w.s.Cmds = append(w.s.Cmds, &Cmd{Name: ":", Redirs: redirs, Stmt: st})
	}
}

func (w *walker) stmts(sts []*syntax.Stmt, depth int) {
	for _, st := range sts {
		w.stmt(st, nil, depth+1)
	}
}

func forWords(c *syntax.ForClause) []*syntax.Word {
	if wi, ok := c.Loop.(*syntax.WordIter); ok {
		return wi.Items
	}
	return nil
}

// words looks for command substitutions inside words and parses their commands too.
func (w *walker) words(ws []*syntax.Word, depth int) bool {
	found := false
	for _, wd := range ws {
		syntax.Walk(wd, func(n syntax.Node) bool {
			switch x := n.(type) {
			case *syntax.CmdSubst:
				found = true
				w.s.HasSubst = true
				w.stmts(x.Stmts, depth+1)
				return false
			case *syntax.ProcSubst:
				found = true
				w.s.HasSubst = true
				w.stmts(x.Stmts, depth+1)
				return false
			}
			return true
		})
	}
	return found
}

func (w *walker) call(st *syntax.Stmt, c *syntax.CallExpr, redirs []Redir, pipeFrom *syntax.Stmt, depth int) {
	cmd := &Cmd{Stmt: st, Redirs: redirs, PipeFrom: pipeFrom}
	for _, a := range c.Assigns {
		if a.Name != nil {
			cmd.Assigns = append(cmd.Assigns, a.Name.Value)
		}
		if a.Value != nil && w.words([]*syntax.Word{a.Value}, depth) {
			cmd.Subst = true
		}
	}
	if w.words(c.Args, depth) {
		cmd.Subst = true
	}
	words := c.Args
	if len(words) == 0 {
		if len(redirs) > 0 {
			cmd.Name = ":"
			w.s.Cmds = append(w.s.Cmds, cmd)
		}
		return
	}
	lits := make([]string, len(words))
	ok := make([]bool, len(words))
	for i, wd := range words {
		lits[i], ok[i] = Lit(wd)
	}

	i := 0
	aliasDone := map[string]bool{}
	for i < len(words) {
		name := lits[i]
		if !ok[i] {
			break
		}
		// alias expansion (not for \name)
		if raw := words[i].Lit(); raw != "" && !strings.HasPrefix(raw, "\\") && w.aliases != nil && !aliasDone[name] {
			if exp, has := w.aliases[name]; has {
				aliasDone[name] = true
				sub, err := Parse(exp, nil)
				if err == nil && len(sub.Cmds) > 0 && sub.Cmds[0].Name != "" {
					first := sub.Cmds[0]
					if cmd.Alias == "" {
						cmd.Alias = name
					}
					// replace the alias word with the expansion's words
					expWords := append([]*syntax.Word{}, firstCallWords(sub)...)
					newWords := append(expWords, words[i+1:]...)
					newLits := make([]string, 0, len(newWords))
					newOk := make([]bool, 0, len(newWords))
					for _, wd := range newWords {
						l, o := Lit(wd)
						newLits = append(newLits, l)
						newOk = append(newOk, o)
					}
					words, lits, ok = newWords, newLits, newOk
					i = 0
					_ = first
					continue
				}
			}
		}
		if wrappersNoArgs[name] {
			if name == "time" || name == "nohup" || name == "exec" {
				cmd.Wrappers = append(cmd.Wrappers, name)
			}
			i++
			continue
		}
		if flags, isWrap := wrappersWithFlags[name]; isWrap {
			cmd.Wrappers = append(cmd.Wrappers, name)
			i++
			for i < len(words) && ok[i] && strings.HasPrefix(lits[i], "-") && lits[i] != "-" && lits[i] != "--" {
				if flags[lits[i]] {
					i++
				}
				i++
			}
			if i < len(words) && lits[i] == "--" {
				i++
			}
			if name == "env" || name == "sudo" {
				for i < len(words) && ok[i] && isAssign(lits[i]) {
					cmd.Assigns = append(cmd.Assigns, strings.SplitN(lits[i], "=", 2)[0])
					i++
				}
			}
			if name == "timeout" && i < len(words) {
				i++ // duration
			}
			if name == "watch" && i == len(words)-1 && ok[i] && strings.Contains(lits[i], " ") {
				// watch 'df -h': parse the quoted command
				if sub, err := Parse(lits[i], w.aliases); err == nil {
					for _, sc := range sub.Cmds {
						sc.Wrappers = append(append([]string{}, cmd.Wrappers...), sc.Wrappers...)
						w.s.Cmds = append(w.s.Cmds, sc)
					}
				}
				return
			}
			continue
		}
		break
	}
	if i >= len(words) {
		if len(cmd.Wrappers) > 0 {
			// bare "sudo -v", "xargs" (echo by default)
			cmd.Name = cmd.Wrappers[len(cmd.Wrappers)-1]
			cmd.Wrappers = cmd.Wrappers[:len(cmd.Wrappers)-1]
			w.s.Cmds = append(w.s.Cmds, cmd)
		}
		return
	}
	name := lits[i]
	if !ok[i] {
		name = "" // dynamic command name, e.g. $EDITOR
	}
	name = strings.TrimPrefix(name, "\\")
	if strings.Contains(name, "/") {
		cmd.Path = name
		name = path.Base(name)
	}
	cmd.Name = name
	cmd.Args = lits[i+1:]
	cmd.Lit = ok[i+1:]
	cmd.Words = words[i+1:]
	w.s.Cmds = append(w.s.Cmds, cmd)

	// ssh host 'remote command': parse the remote part as its own commands
	if name == "ssh" {
		if host, rest := sshSplit(cmd.Args, cmd.Lit); host != "" && rest != "" {
			cmd.Remote = host
			if sub, err := Parse(rest, nil); err == nil {
				for _, sc := range sub.Cmds {
					sc.Remote = host
					w.s.Cmds = append(w.s.Cmds, sc)
				}
			}
		}
	}
}

func firstCallWords(s *Script) []*syntax.Word {
	for _, st := range s.File.Stmts {
		if c, ok := st.Cmd.(*syntax.CallExpr); ok {
			return c.Args
		}
	}
	return nil
}

func isAssign(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range s[:eq] {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// ssh flags that take a value
var sshValueFlags = "bcDEeFIiJLlmOopQRSWw"

// sshSplit returns the host and the remote command of an ssh invocation.
func sshSplit(args []string, lit []bool) (host, rest string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if !lit[i] {
			return "", ""
		}
		if strings.HasPrefix(a, "-") && len(a) >= 2 {
			last := a[len(a)-1:]
			if len(a) == 2 && strings.Contains(sshValueFlags, last) {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return "", ""
	}
	host = args[i]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	if i+1 < len(args) {
		rest = strings.Join(args[i+1:], " ")
	}
	return host, rest
}

func convRedirs(rs []*syntax.Redirect) []Redir {
	var out []Redir
	for _, r := range rs {
		op := r.Op.String()
		if r.N != nil {
			op = r.N.Value + op
		}
		var t string
		var ok bool
		if r.Word != nil {
			t, ok = Lit(r.Word)
		}
		out = append(out, Redir{Op: op, Target: t, Lit: ok})
	}
	return out
}

// Lit returns the literal value of a word if it has no expansions (quotes are removed;
// a leading ~ is kept as-is).
func Lit(w *syntax.Word) (string, bool) {
	var sb strings.Builder
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			sb.WriteString(unescape(x.Value))
		case *syntax.SglQuoted:
			sb.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				l, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				sb.WriteString(l.Value)
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	esc := false
	for _, r := range s {
		if esc {
			sb.WriteRune(r)
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// HasGlob reports whether a literal argument contains glob characters.
func HasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

// Flags returns the set of flags in args: "-rf" -> r, f; "--force" -> --force.
func Flags(args []string) map[string]bool {
	f := map[string]bool{}
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--") {
			f[strings.SplitN(a, "=", 2)[0]] = true
		} else if strings.HasPrefix(a, "-") && len(a) > 1 {
			for _, r := range a[1:] {
				f["-"+string(r)] = true
			}
		}
	}
	return f
}

// Operands returns the non-flag arguments (everything after "--" counts as operand).
func Operands(args []string) []string {
	var out []string
	dd := false
	for _, a := range args {
		if !dd && a == "--" {
			dd = true
			continue
		}
		if !dd && strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// String renders a command for messages.
func (c *Cmd) String() string {
	parts := append([]string{}, c.Wrappers...)
	n := c.Name
	if c.Path != "" {
		n = c.Path
	}
	parts = append(parts, n)
	for i, a := range c.Args {
		if !c.Lit[i] {
			a = "…"
		}
		parts = append(parts, a)
	}
	s := strings.Join(parts, " ")
	if c.Remote != "" {
		s = fmt.Sprintf("[%s] %s", c.Remote, s)
	}
	return s
}

// Print renders an AST node back to source.
func Print(n syntax.Node) string {
	var sb strings.Builder
	syntax.NewPrinter(syntax.Minify(false)).Print(&sb, n)
	return strings.TrimSpace(sb.String())
}
