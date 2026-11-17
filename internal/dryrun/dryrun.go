// Package dryrun resolves what a move/delete would touch, with read-only probes
// (PLAN.md §2.3, §2.6–2.8). The question shown to the user is built from this,
// never from the model's text.
package dryrun

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/MariusEng/zshai/internal/azcat"
	"github.com/MariusEng/zshai/internal/risk"
	"github.com/MariusEng/zshai/internal/shell"
)

type Item struct {
	Path  string `json:"path"`
	Dest  string `json:"dest,omitempty"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"dir,omitempty"`
	Files int    `json:"files"`
}

type Plan struct {
	Kind      string            `json:"kind"`
	Items     []Item            `json:"items,omitempty"`
	Dest      string            `json:"dest,omitempty"`
	Files     int               `json:"files"`
	Dirs      int               `json:"dirs"`
	Bytes     int64             `json:"bytes"`
	Oldest    time.Time         `json:"oldest"`
	Newest    time.Time         `json:"newest"`
	Conflicts []string          `json:"conflicts,omitempty"`
	Missing   []string          `json:"missing,omitempty"`
	Lines     []string          `json:"lines,omitempty"`
	Facts     map[string]string `json:"facts,omitempty"`
	Trashable bool              `json:"trashable"` // zshai can do this delete itself, to the trash
	Unknown   bool              `json:"unknown"`   // targets could not be resolved
	Why       string            `json:"why,omitempty"`
	Raise     bool              `json:"raise"` // raise the level to delete (overwrite of an existing file)
	Repo      string            `json:"repo,omitempty"`
	Remote    string            `json:"remote,omitempty"`
}

type Env struct {
	Cwd     string
	Home    string
	Az      *azcat.Catalog
	Timeout time.Duration
}

// Build makes a plan for the most important finding that has a dry-run kind.
func Build(s *shell.Script, r risk.Result, env Env) *Plan {
	if env.Timeout == 0 {
		env.Timeout = 5 * time.Second
	}
	var best *risk.Finding
	for i := range r.Findings {
		f := &r.Findings[i]
		if f.Kind == "" {
			continue
		}
		if best == nil || f.Level > best.Level {
			best = f
		}
	}
	if best == nil {
		return nil
	}
	c := best.Cmd
	single := len(s.File.Stmts) == 1 && len(s.Cmds) == 1
	p := &Plan{Kind: best.Kind, Facts: map[string]string{}, Remote: c.Remote}
	if c.Remote != "" {
		p.Unknown = true
		p.Why = "remote"
		p.Lines = append(p.Lines, c.String())
		return p
	}
	switch best.Kind {
	case "rm":
		paths, ok := expandOperands(c, env)
		if !ok {
			p.Unknown = true
			p.Why = "the file list is computed by a command"
			return p
		}
		p.addPaths(paths, env)
		p.Trashable = single && c.Name != "trash"
	case "mv":
		paths, ok := expandOperands(c, env)
		if !ok || len(paths) < 2 {
			p.Unknown = true
			p.Why = "could not resolve the files to move"
			return p
		}
		dest := paths[len(paths)-1]
		p.Dest = dest
		fi, err := os.Stat(abs(dest, env.Cwd))
		destIsDir := err == nil && fi.IsDir() || len(paths) > 2 || strings.HasSuffix(dest, "/")
		p.addPaths(paths[:len(paths)-1], env)
		for i := range p.Items {
			if destIsDir {
				p.Items[i].Dest = filepath.Join(dest, filepath.Base(p.Items[i].Path))
			} else {
				p.Items[i].Dest = dest
			}
			if _, err := os.Lstat(abs(p.Items[i].Dest, env.Cwd)); err == nil {
				p.Conflicts = append(p.Conflicts, p.Items[i].Dest)
			}
		}
		if err != nil && !destIsDir {
			p.Facts["rename"] = "1"
		}
	case "find-delete":
		args, ok := literalArgs(c)
		if !ok {
			p.Unknown = true
			p.Why = "find arguments are computed"
			return p
		}
		probe := stripFindActions(args)
		out, err := run(env, env.Cwd, c.Name, append(probe, "-print0")...)
		if err != nil && len(out) == 0 {
			p.Unknown = true
			p.Why = "find failed: " + firstLine(err.Error())
			return p
		}
		p.addPaths(splitNul(out), env)
		p.Trashable = single
	case "xargs-rm":
		if c.PipeFrom == nil {
			p.Unknown = true
			return p
		}
		left := shell.Print(c.PipeFrom)
		ls, _ := shell.Parse(left, nil)
		if ls == nil || risk.Classify(ls, risk.Env{Home: env.Home}).Level != risk.Read {
			p.Unknown = true
			p.Why = "the file list comes from a command that changes things"
			return p
		}
		out, _ := run(env, env.Cwd, "zsh", "-f", "-c", left)
		var paths []string
		if bytes.IndexByte(out, 0) >= 0 {
			paths = splitNul(out)
		} else {
			paths = strings.Fields(string(out))
		}
		p.addPaths(paths, env)
		p.Trashable = len(s.File.Stmts) == 1 && len(s.Cmds) == 2
	case "git-clean":
		_, args := risk.GitSub(c.Args)
		probe := []string{"clean", "-n"}
		for _, a := range args {
			if a == "-f" || a == "-ff" || a == "--force" || a == "-i" || a == "--interactive" {
				continue
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
				a = strings.NewReplacer("f", "", "i", "").Replace(a)
				if a == "-" {
					continue
				}
			}
			probe = append(probe, a)
		}
		out, err := run(env, env.Cwd, "git", probe...)
		if err != nil {
			p.Unknown = true
			p.Why = "git clean -n failed"
			return p
		}
		var paths []string
		for _, l := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(l, "Would remove "); ok {
				paths = append(paths, strings.TrimSuffix(f, "/"))
			}
		}
		p.Repo = gitRoot(env)
		p.addPaths(paths, env)
		p.Trashable = single
	case "git-reset":
		p.Repo = gitRoot(env)
		out, _ := run(env, env.Cwd, "git", "status", "--porcelain")
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if len(l) > 3 && !strings.HasPrefix(l, "??") {
				p.Lines = append(p.Lines, strings.TrimSpace(l[3:]))
			}
		}
		sub, args := risk.GitSub(c.Args)
		if sub == "reset" {
			ops := shell.Operands(args)
			target := "HEAD"
			if len(ops) > 0 {
				target = ops[0]
			}
			if target != "HEAD" {
				n := gitCount(env, "rev-list", "--count", target+"..HEAD")
				p.Facts["commits"] = strconv.Itoa(n)
				if n > 0 {
					p.Facts["unpushed"] = strconv.Itoa(gitCount(env, "rev-list", "--count", target+"..HEAD", "--not", "--remotes"))
				}
			}
		}
		p.Files = len(p.Lines)
	case "git-branch-D":
		p.Repo = gitRoot(env)
		_, args := risk.GitSub(c.Args)
		for _, br := range shell.Operands(args) {
			n := gitCount(env, "rev-list", "--count", br, "--not", "--exclude="+br, "--branches", "--remotes")
			p.Lines = append(p.Lines, fmt.Sprintf("%s\t%d", br, n))
		}
	case "git-push-force":
		p.Repo = gitRoot(env)
		p.Facts["overwritten"] = strconv.Itoa(gitCount(env, "rev-list", "--count", "HEAD..@{u}"))
		p.Facts["pushing"] = strconv.Itoa(gitCount(env, "rev-list", "--count", "@{u}..HEAD"))
	case "az-delete":
		name := risk.AzName(c, env.Az)
		p.Facts["cmd"] = name
		target := risk.OptValue(c.Args, "--name", "-n", "--ids")
		group := risk.OptValue(c.Args, "--resource-group", "-g")
		if name == "group delete" {
			group = target
			if group == "" {
				group = risk.OptValue(c.Args, "--resource-group", "-g")
			}
			p.Facts["group"] = group
			out, err := run(Env{Timeout: 30 * time.Second}, env.Cwd, "az", "resource", "list", "-g", group, "--query", "[].type", "-o", "tsv")
			if err != nil {
				p.Unknown = true
				p.Why = "could not list the group's resources: " + firstLine(err.Error())
				return p
			}
			counts := map[string]int{}
			for _, t := range strings.Fields(string(out)) {
				counts[t]++
			}
			p.Files = len(strings.Fields(string(out)))
			for t, n := range counts {
				p.Lines = append(p.Lines, fmt.Sprintf("%d× %s", n, t))
			}
			sort.Slice(p.Lines, func(i, j int) bool { return p.Lines[i] > p.Lines[j] })
		} else {
			p.Facts["target"] = target
			p.Facts["group"] = group
		}
	case "pkg-remove":
		p.Lines = pkgRemovals(c, env)
		if len(p.Lines) == 0 {
			p.Unknown = true
			p.Why = "the package manager's dry run listed nothing"
		}
	case "truncate":
		for _, rd := range c.Redirs {
			op := strings.TrimLeft(rd.Op, "0123456789&")
			if (op == ">" || op == ">|" || op == ">!") && rd.Lit {
				if fi, err := os.Stat(abs(rd.Target, env.Cwd)); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
					p.Raise = true
					p.addPaths([]string{rd.Target}, env)
				}
			}
		}
		if !p.Raise {
			return nil
		}
	case "rsync-delete":
		args, ok := literalArgs(c)
		if !ok {
			p.Unknown = true
			return p
		}
		for _, a := range args {
			if strings.Contains(a, ":") && !strings.HasPrefix(a, "-") {
				p.Unknown = true
				p.Why = "remote rsync"
				return p
			}
		}
		out, _ := run(env, env.Cwd, "rsync", append([]string{"--dry-run", "--itemize-changes"}, args...)...)
		for _, l := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(l, "*deleting "); ok {
				p.Lines = append(p.Lines, strings.TrimSpace(f))
			}
		}
		p.Files = len(p.Lines)
	default:
		return nil
	}
	return p
}

func expandOperands(c *shell.Cmd, env Env) ([]string, bool) {
	if c.Subst {
		return nil, false
	}
	cfg := &expand.Config{
		Env:      expand.ListEnviron(os.Environ()...),
		ReadDir2: os.ReadDir,
		GlobStar: true,
		NullGlob: true,
	}
	var out []string
	dd := false
	for i, w := range c.Words {
		if c.Lit[i] && !dd {
			a := c.Args[i]
			if a == "--" {
				dd = true
				continue
			}
			if strings.HasPrefix(a, "-") && a != "-" {
				continue
			}
		}
		if src := wordSrc(w); zshQualRe.MatchString(src) {
			// a zsh glob qualifier (zshai*(.) = plain files): only zsh knows what it matches
			fields, ok := zshGlob(src, env.Cwd)
			if !ok {
				return nil, false
			}
			out = append(out, fields...)
			continue
		}
		fields, err := expand.Fields(cfg, w)
		if err != nil {
			return nil, false
		}
		out = append(out, fields...)
	}
	return out, true
}

// zshQualRe: a glob with a zsh qualifier at the end: *(.) *(/) *(.om[1,5]) *(N.mh-1)
var zshQualRe = regexp.MustCompile(`[*?\]][^/]*\(([^()]*)\)$`)

// +name in a qualifier calls a shell function
var qualFuncRe = regexp.MustCompile(`\+[A-Za-z_]`)

func wordSrc(w *syntax.Word) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, w)
	return b.String()
}

// zshGlob expands one glob word with zsh, in the folder. Only qualifiers that select
// files: e:…: and +func run code, and $ / ` would expand more than a glob, so those are
// not planned (the dry run then says it can't tell what the command touches).
func zshGlob(src, cwd string) ([]string, bool) {
	m := zshQualRe.FindStringSubmatch(src)
	if m == nil {
		return nil, false
	}
	q := m[1]
	if strings.ContainsAny(src, "$`'\"\\\n;|&<>") || strings.ContainsAny(q, "eP") || qualFuncRe.MatchString(q) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zsh", "-f", "-c", "setopt nullglob; print -rN -- "+src)
	cmd.Dir = cwd
	b, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var out []string
	for _, f := range strings.Split(string(b), "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out, true
}

func literalArgs(c *shell.Cmd) ([]string, bool) {
	for _, ok := range c.Lit {
		if !ok {
			return nil, false
		}
	}
	return c.Args, true
}

// stripFindActions removes -delete and -exec … ; / + so find only lists.
func stripFindActions(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-delete", "-print", "-print0", "-ls":
			continue
		case "-exec", "-execdir", "-ok", "-okdir":
			for i++; i < len(args) && args[i] != ";" && args[i] != "+" && args[i] != `\;`; i++ {
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func (p *Plan) addPaths(paths []string, env Env) {
	seen := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		full := abs(path, env.Cwd)
		fi, err := os.Lstat(full)
		if err != nil {
			p.Missing = append(p.Missing, path)
			continue
		}
		it := Item{Path: path}
		if fi.IsDir() {
			it.IsDir = true
			p.Dirs++
			_ = filepath.WalkDir(full, func(q string, d fs.DirEntry, err error) error {
				if err != nil || time.Now().After(deadline) {
					return fs.SkipAll
				}
				if d.IsDir() {
					return nil
				}
				if info, err := d.Info(); err == nil {
					it.Files++
					it.Size += info.Size()
					p.see(info.ModTime())
				}
				return nil
			})
		} else {
			it.Files = 1
			it.Size = fi.Size()
			p.see(fi.ModTime())
		}
		p.Files += it.Files
		p.Bytes += it.Size
		p.Items = append(p.Items, it)
	}
}

func (p *Plan) see(t time.Time) {
	if p.Oldest.IsZero() || t.Before(p.Oldest) {
		p.Oldest = t
	}
	if t.After(p.Newest) {
		p.Newest = t
	}
}

func abs(p, cwd string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

func run(env Env, dir, name string, args ...string) ([]byte, error) {
	t := env.Timeout
	if t == 0 {
		t = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return out, err
}

func splitNul(b []byte) []string {
	var out []string
	for _, s := range bytes.Split(b, []byte{0}) {
		if len(s) > 0 {
			out = append(out, string(s))
		}
	}
	return out
}

var errCode = regexp.MustCompile(`\(([A-Z][A-Za-z]+)\)`)

// firstLine shortens an error for the question: Azure errors become their code.
func firstLine(s string) string {
	if m := errCode.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func gitRoot(env Env) string {
	out, err := run(env, env.Cwd, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitCount(env Env, args ...string) int {
	out, err := run(env, env.Cwd, "git", args...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func pkgRemovals(c *shell.Cmd, env Env) []string {
	ops := shell.Operands(c.Args)
	switch c.Name {
	case "apt", "apt-get":
		if len(ops) == 0 {
			return nil
		}
		out, _ := run(env, env.Cwd, "apt-get", append([]string{"-s"}, ops...)...)
		var names []string
		for _, l := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(l, "Remv "); ok {
				names = append(names, strings.Fields(f)[0])
			} else if f, ok := strings.CutPrefix(l, "Purg "); ok {
				names = append(names, strings.Fields(f)[0])
			}
		}
		return names
	case "pacman":
		var op string
		var pkgs []string
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-R") {
				op = a
			} else if !strings.HasPrefix(a, "-") {
				pkgs = append(pkgs, a)
			}
		}
		if op == "" {
			return nil
		}
		out, _ := run(env, env.Cwd, "pacman", append([]string{op + "p", "--print-format", "%n"}, pkgs...)...)
		return strings.Fields(string(out))
	case "brew":
		if len(ops) > 0 && ops[0] == "autoremove" {
			out, _ := run(env, env.Cwd, "brew", "autoremove", "--dry-run")
			var names []string
			for _, l := range strings.Split(string(out), "\n") {
				if l != "" && !strings.HasPrefix(l, "==>") {
					names = append(names, strings.TrimSpace(l))
				}
			}
			return names
		}
		if len(ops) > 1 {
			return ops[1:]
		}
	}
	if len(ops) > 1 {
		return ops[1:]
	}
	return nil
}

// Print renders a node (re-exported for callers that only import dryrun).
func Print(n syntax.Node) string { return shell.Print(n) }
