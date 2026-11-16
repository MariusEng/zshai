// Package validate checks a generated command before it is shown (PLAN.md §2):
// syntax (mvdan + zsh -n), every command exists, git subcommands, az commands and
// options against the catalog, GNU/BSD flag traps, brew subcommand typos.
package validate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/azcat"
	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/risk"
	"github.com/MariusEng/zshai/internal/shell"
	"github.com/MariusEng/zshai/internal/tools"
)

type Result struct {
	Problems []string // send back to the model
	Missing  []string // common tools that are not installed (install suggestion)
	Warnings []string // shown to the user, not blocking
}

func (r Result) OK() bool { return len(r.Problems) == 0 && len(r.Missing) == 0 }

type Env struct {
	OS        string
	Cwd       string
	Aliases   shell.Aliases
	Functions map[string]bool
	Az        *azcat.Catalog
	AzDef     azcat.Defaults
}

// zsh builtins and reserved words (zsh 5.9)
var builtins = map[string]bool{}

func init() {
	for _, b := range strings.Fields(`- . : [ [[ alias autoload bg bindkey break builtin bye cd chdir command compadd
	comparguments compcall compctl compdescribe compfiles compgroups compquote comptags comptry compvalues continue
	declare dirs disable disown echo echotc echoti emulate enable eval exec exit export false fc fg float functions
	getln getopts hash history integer jobs kill let limit local log logout noglob popd print printf private pushd
	pushln pwd r read readonly rehash return sched set setopt shift source suspend test times trap true ttyctl type
	typeset ulimit umask unalias unfunction unhash unlimit unset unsetopt vared wait whence where which zcompile zle
	zmodload zparseopts zregexparse zstyle time if then else elif fi case esac for select while until do done
	function repeat coproc nocorrect foreach end`) {
		builtins[b] = true
	}
}

// Check validates script.
func Check(s *shell.Script, env Env) Result {
	var r Result
	if err := zshSyntax(s.Src); err != nil {
		r.Problems = append(r.Problems, "zsh syntax error: "+err.Error())
	}
	seenMissing := map[string]bool{}
	for _, c := range s.Cmds {
		if c.Name == "" || c.Name == ":" || c.Remote != "" {
			continue
		}
		if !exists(c, env) {
			if t, ok := tools.Lookup(c.Name); ok {
				if !seenMissing[t.Name] {
					seenMissing[t.Name] = true
					r.Missing = append(r.Missing, t.Name)
				}
				continue
			}
			r.Problems = append(r.Problems, fmt.Sprintf("command not found: %s", c.Name))
			continue
		}
		r.Problems = append(r.Problems, flagTraps(c, env.OS)...)
		r.Problems = append(r.Problems, awkCheck(c)...)
		r.Problems = append(r.Problems, sqliteCheck(c, env.Cwd)...)
		r.Problems = append(r.Problems, jqCheck(c, env.Cwd)...)
		r.Problems = append(r.Problems, quotedTilde(c)...)
		switch c.Name {
		case "zshai":
			r.Problems = append(r.Problems, fmtCheck(c, env.Cwd)...)
		case "git":
			if p := gitSub(c); p != "" {
				r.Problems = append(r.Problems, p)
			}
		case "az":
			if env.Az != nil {
				r.Problems = append(r.Problems, env.Az.Validate(c.Args, env.AzDef)...)
				if name, cmd, _ := env.Az.Resolve(c.Args); cmd != nil && cmd.Deprecated {
					r.Warnings = append(r.Warnings, "az "+name+" is deprecated")
				}
			}
		case "brew":
			if p := brewSub(c); p != "" {
				r.Problems = append(r.Problems, p)
			}
		case "pacman":
			for _, a := range c.Args {
				if strings.HasPrefix(a, "-S") && strings.Contains(a, "y") && !strings.Contains(a, "u") && len(shell.Operands(c.Args)) > 0 {
					r.Problems = append(r.Problems, "pacman -Sy <pkg> is a partial upgrade; use -S <pkg> (or -Syu to update everything)")
				}
			}
		}
	}
	r.Problems = append(r.Problems, jqToColumn(s)...)
	if len(r.Problems) == 0 && len(r.Missing) == 0 {
		r.Problems = append(r.Problems, TryRun(s, env.Cwd)...)
	}
	return r
}

func zshSyntax(src string) error {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-n", "-f", "-c", src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		msg = strings.TrimPrefix(msg, "zsh:")
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", strings.TrimSpace(msg))
	}
	return nil
}

func exists(c *shell.Cmd, env Env) bool {
	if builtins[c.Name] || env.Functions[c.Name] || c.Name == "zshai" && c.Path == "" {
		return true
	}
	if _, ok := env.Aliases[c.Name]; ok && c.Path == "" {
		return true
	}
	if c.Path != "" {
		p := c.Path
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, p[2:])
		} else if !filepath.IsAbs(p) {
			p = filepath.Join(env.Cwd, p)
		}
		_, err := os.Stat(p)
		return err == nil
	}
	p, err := exec.LookPath(c.Name)
	if err != nil {
		return false
	}
	// macOS file systems ignore case: "Head" finds /usr/bin/head, but zsh's command hash
	// and other machines don't. Only the exact name counts.
	if c.Name != strings.ToLower(c.Name) {
		return exactName(filepath.Dir(p), c.Name)
	}
	return true
}

func exactName(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}

type trap struct {
	os, cmd, flag, why string
}

// GNU/BSD differences that silently do the wrong thing or fail (PLAN.md §2.4).
var traps = []trap{
	{"darwin", "stat", "-c", "BSD stat has no -c; use stat -f (e.g. stat -f %z for size)"},
	{"darwin", "date", "-d", "BSD date has no -d; use date -v (e.g. date -v-7d) or date -j -f"},
	{"darwin", "find", "-printf", "BSD find has no -printf; use -exec stat -f … or -print"},
	{"darwin", "du", "--max-depth", "BSD du: use -d N instead of --max-depth"},
	{"darwin", "grep", "-P", "BSD grep has no -P; use -E"},
	{"darwin", "ls", "--time-style", "BSD ls: use -D format instead of --time-style"},
	{"darwin", "sort", "--parallel", "BSD sort has no --parallel"},
	{"darwin", "sed", "-r", "BSD sed: use -E instead of -r"},
	{"linux", "stat", "-f", "GNU stat -f shows the file system, not the file; use stat -c"},
	{"linux", "date", "-v", "GNU date has no -v; use date -d '7 days ago'"},
	{"linux", "date", "-j", "GNU date has no -j; use date -d"},
	{"linux", "base64", "-D", "GNU base64: use -d to decode"},
}

func flagTraps(c *shell.Cmd, goos string) []string {
	if goos == "" {
		goos = runtime.GOOS
	}
	var out []string
	f := shell.Flags(c.Args)
	for _, t := range traps {
		if t.os == goos && t.cmd == c.Name && f[t.flag] {
			out = append(out, fmt.Sprintf("%s %s: %s", c.Name, t.flag, t.why))
		}
	}
	// BSD ps: no long options at all, and the column is "command", not "cmd"
	if c.Name == "ps" && goos == "darwin" {
		for i, a := range c.Args {
			if strings.HasPrefix(a, "--") {
				out = append(out, "BSD ps has no long options like "+a+"; sort with ps -axm (memory) / -axr (CPU), "+
					"hide the header with = after each column: ps -axo pid=,ppid=,command=")
				break
			}
			if a == "-o" && i+1 < len(c.Args) || strings.HasPrefix(a, "-") && strings.HasSuffix(a, "o") && i+1 < len(c.Args) {
				for _, k := range strings.Split(c.Args[i+1], ",") {
					if k = strings.TrimSuffix(k, "="); k == "cmd" {
						out = append(out, "BSD ps: the column is command (or comm), not cmd")
					}
				}
			}
		}
	}
	// sed -i: BSD needs a suffix argument, GNU must not get a separate '' argument
	if c.Name == "sed" {
		for i, a := range c.Args {
			if a != "-i" {
				continue
			}
			next := ""
			if i+1 < len(c.Args) {
				next = c.Args[i+1]
			}
			if goos == "darwin" && strings.HasPrefix(next, "s") && len(next) > 1 && !strings.HasPrefix(next, "-") && strings.ContainsAny(next[1:2], "/|#,:") {
				out = append(out, "BSD sed -i needs a suffix argument: sed -i '' 's/…/…/' file")
			}
			if goos == "linux" && i+1 < len(c.Lit) && c.Lit[i+1] && next == "" {
				out = append(out, "GNU sed: write sed -i 's/…/…/' file (no '' after -i)")
			}
		}
	}
	return out
}

// ---- git subcommands, cached per git version

func gitSub(c *shell.Cmd) string {
	sub, _ := risk.GitSub(c.Args)
	if sub == "" {
		return ""
	}
	cmds := gitCommands()
	if len(cmds) == 0 || cmds[sub] {
		return ""
	}
	best, bd := "", 3
	for k := range cmds {
		if d := lev(sub, k); d < bd {
			best, bd = k, d
		}
	}
	if best != "" {
		return fmt.Sprintf("git %s is not a git command (did you mean git %s?)", sub, best)
	}
	return fmt.Sprintf("git %s is not a git command", sub)
}

func gitCommands() map[string]bool {
	ver, err := exec.Command("git", "--version").Output()
	if err != nil {
		return nil
	}
	cacheFile := filepath.Join(config.CacheDir(), "git-cmds-"+strings.ReplaceAll(strings.TrimSpace(string(ver)), " ", "_"))
	b, err := os.ReadFile(cacheFile)
	if err != nil {
		b, err = exec.Command("git", "--list-cmds=main,others,alias,nohelpers").Output()
		if err != nil {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(cacheFile), 0o755)
		_ = os.WriteFile(cacheFile, b, 0o644)
	}
	m := map[string]bool{}
	for _, l := range strings.Fields(string(b)) {
		m[l] = true
	}
	// git-lfs etc. come from PATH as git-<name>
	return m
}

// ---- brew

var brewCmds = strings.Fields(`install reinstall uninstall remove rm upgrade update list ls info search outdated
	deps uses leaves config doctor cleanup autoremove tap untap tap-info services pin unpin link unlink home desc
	log cat edit fetch shellenv commands analytics bundle missing migrate readall postinstall --prefix --cellar
	--cache --repository --version --caskroom which-formula alias developer completions casks formulae gist-logs
	install-bundler-gems nodenv-sync options pyenv-sync rbenv-sync setup-ruby tab update-reset vendor-install`)

func brewSub(c *shell.Cmd) string {
	ops := shell.Operands(c.Args)
	sub := ""
	for _, a := range c.Args {
		if strings.HasPrefix(a, "--") && slices.Contains(brewCmds, a) {
			return ""
		}
	}
	if len(ops) > 0 {
		sub = ops[0]
	}
	if sub == "" || slices.Contains(brewCmds, sub) {
		return ""
	}
	best, bd := "", 3
	for _, k := range brewCmds {
		if d := lev(sub, k); d < bd {
			best, bd = k, d
		}
	}
	if best != "" {
		return fmt.Sprintf("brew %s is not a brew command (did you mean brew %s?)", sub, best)
	}
	return ""
}

func lev(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
