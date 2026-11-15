package validate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MariusEng/zshai/internal/shell"
)

// awkProgram returns the inline program of an awk command ("" if there is none or
// it is not a plain literal, e.g. awk -f file or a program built from variables).
func awkProgram(c *shell.Cmd) string {
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		switch {
		case a == "--":
			if i+1 < len(c.Args) && c.Lit[i+1] {
				return c.Args[i+1]
			}
			return ""
		case a == "-f" || strings.HasPrefix(a, "--file") || (strings.HasPrefix(a, "-f") && !strings.HasPrefix(a, "-F")):
			return ""
		case a == "-F" || a == "-v":
			i++
		case strings.HasPrefix(a, "-") && a != "-":
			// -F: or -vx=1 or a long option
		default:
			if c.Lit[i] {
				return a
			}
			return ""
		}
	}
	return ""
}

var (
	awkPrintKw  = regexp.MustCompile(`\bprintf?\b`)
	awkBeginEnd = regexp.MustCompile(`\b(BEGIN|END|BEGINFILE|ENDFILE)\b`)
)

// awkPrintRedirect finds the classic mistake  print $1 > 0 ? "yes" : "no"  where the
// model meant a comparison: inside print, an unparenthesized > is output redirection
// (here: to a file named "0"). BWK awk (macOS) rejects it, gawk writes the file.
func awkPrintRedirect(prog string) string {
	for _, loc := range awkPrintKw.FindAllStringIndex(prog, -1) {
		depth, inStr := 0, false
		for i := loc[1]; i < len(prog); i++ {
			ch := prog[i]
			if inStr {
				if ch == '\\' {
					i++
				} else if ch == '"' {
					inStr = false
				}
				continue
			}
			switch ch {
			case '"':
				inStr = true
			case '(':
				depth++
			case ')':
				depth--
			case ';', '}', '\n':
				if depth <= 0 {
					i = len(prog)
				}
			case '>':
				if depth > 0 {
					continue
				}
				rest := strings.TrimLeft(strings.TrimPrefix(prog[i+1:], ">"), " \t")
				end := strings.IndexAny(rest, ";}\n")
				if end < 0 {
					end = len(rest)
				}
				if (rest != "" && rest[0] >= '0' && rest[0] <= '9') || strings.Contains(rest[:end], "?") {
					return `awk: inside print, an unparenthesized ">" redirects output to a file; ` +
						`for a comparison put it in parentheses, e.g. print ($1 > 0 ? "yes" : "no")`
				}
				i = len(prog)
			}
		}
	}
	return ""
}

// awkSyntax lets the local awk parse the program. That is only side-effect free when
// nothing runs without input: no BEGIN/END blocks (which run even on empty input) and no
// gawk @load/@include. Main rules never run because the input is /dev/null.
func awkSyntax(name, prog string) string {
	if awkBeginEnd.MatchString(prog) || strings.Contains(prog, "@") {
		return ""
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	dir, err := os.MkdirTemp("", "zshai-awk-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--", prog)
	cmd.Dir = dir
	cmd.Stdin = nil // /dev/null
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() == nil || ctx.Err() != nil {
		return ""
	}
	msg := strings.Join(strings.Fields(stderr.String()), " ")
	if msg == "" {
		return ""
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return name + " syntax error: " + msg
}

var (
	gawkOnly = regexp.MustCompile(`\b(asort|asorti|gensub|patsplit|systime|mktime|strftime|and|or|xor|lshift|rshift|compl|isarray|typeof)\s*\(`)
	gawkMu   sync.Mutex
	gawkSeen = map[string]bool{}
)

// isGawk: whether this awk is GNU awk (macOS awk is BWK awk, Ubuntu's default is mawk).
func isGawk(name string) bool {
	if name == "gawk" {
		return true
	}
	gawkMu.Lock()
	defer gawkMu.Unlock()
	if v, ok := gawkSeen[name]; ok {
		return v
	}
	out, _ := exec.Command(name, "--version").CombinedOutput()
	v := strings.Contains(string(out), "GNU Awk")
	gawkSeen[name] = v
	return v
}

func awkCheck(c *shell.Cmd) []string {
	switch c.Name {
	case "awk", "gawk", "mawk", "nawk":
	default:
		return nil
	}
	prog := awkProgram(c)
	if prog == "" {
		return nil
	}
	var out []string
	if m := gawkOnly.FindStringSubmatch(prog); m != nil && !isGawk(c.Name) {
		out = append(out, c.Name+" here is not GNU awk and has no "+m[1]+"(); sort with sort(1) outside awk instead "+
			"(e.g. (head -n 1 f.csv; tail -n +2 f.csv | sort -t, -k4 -nr))")
	}
	if p := awkPrintRedirect(prog); p != "" {
		out = append(out, p)
	}
	if p := awkSyntax(c.Name, prog); p != "" {
		out = append(out, p)
	}
	return out
}
