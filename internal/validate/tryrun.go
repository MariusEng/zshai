package validate

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/shell"
)

// Commands that only read files and write to stdout (with the flags that would change
// that excluded below). A pipeline made only of these can be run once to see if it works.
var readOnly = map[string]bool{
	"jq": true, "column": true, "sort": true, "head": true, "tail": true, "cut": true, "tr": true,
	"uniq": true, "wc": true, "grep": true, "egrep": true, "cat": true, "nl": true, "paste": true,
	"rev": true, "fold": true, "fmt": true, "expand": true, "unexpand": true, "comm": true, "join": true,
}

func readOnlyCmd(c *shell.Cmd) bool {
	if !readOnly[c.Name] || c.Subst || c.Remote != "" || len(c.Wrappers) > 0 || c.Alias != "" {
		return false
	}
	for _, r := range c.Redirs {
		if r.Op != "<" { // only reading a file
			return false
		}
	}
	for i, a := range c.Args {
		if !c.Lit[i] {
			return false
		}
		switch c.Name {
		case "sort":
			if a == "-o" || strings.HasPrefix(a, "--output") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "o")) {
				return false
			}
		case "tail":
			if a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow") || (strings.HasPrefix(a, "-") && strings.ContainsAny(a, "fF")) {
				return false
			}
		case "jq":
			if a == "-f" || a == "--from-file" || a == "-L" {
				return false
			}
		}
	}
	return true
}

// TryRun runs a pipeline of read-only commands once in the cwd (stdin /dev/null, stdout thrown
// away, 1.5 s: a slow pipeline is not worth the wait) and reports its errors: jq … | jq … with the wrong filter, sort on a missing column.
func TryRun(s *shell.Script, cwd string) []string {
	if len(s.Cmds) < 2 || s.HasLoop || s.HasFunc || s.HasSubst || s.File == nil || len(s.File.Stmts) != 1 {
		return nil
	}
	for _, c := range s.Cmds {
		if !readOnlyCmd(c) {
			return nil
		}
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-f", "-o", "pipefail", "-c", s.Src)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		if err == nil {
			return jqRowsCheck(s, zsh, cwd) // ran fine; but does jq feed column text rows?
		}
		return nil // e.g. grep found nothing: not an error
	}
	var lines []string
	seen := map[string]bool{}
	for _, l := range strings.Split(msg, "\n") {
		key := l
		if i := strings.Index(l, "): "); i > 0 {
			key = l[i+3:]
		}
		if l = strings.TrimSpace(l); l == "" || seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, l)
		if len(lines) == 3 {
			break
		}
	}
	return []string{"running it on the real files failed: " + strings.Join(lines, " | ")}
}

// jqRowsCheck runs the jq part in front of column on its own: if it prints JSON ("[", "{")
// instead of text rows, column turns it into a mess of brackets (jq -r '[[.a,.b] | @tsv]').
func jqRowsCheck(s *shell.Script, zsh, cwd string) []string {
	for _, c := range s.Cmds {
		if c.Name != "column" || c.PipeFrom == nil {
			continue
		}
		left := shell.Print(c.PipeFrom)
		if !strings.Contains(left, "jq ") {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		cmd := exec.CommandContext(ctx, zsh, "-f", "-c", left)
		cmd.Dir = cwd
		out, _ := cmd.Output()
		cancel()
		t := strings.TrimSpace(string(out))
		lines := strings.Split(t, "\n")
		if n := strings.Count("\n"+t+"\n", "\n"+lines[0]+"\n"); n > 1 && len(lines) > 2 && strings.Contains(lines[0], "\t") {
			return []string{fmt.Sprintf("the header row comes out %d times (once per object): take the keys once, "+
				"(.[0] | keys_unsorted) as $k, not (.[] | keys_unsorted) as $k", n)}
		}
		if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") || strings.HasPrefix(t, `"`) {
			first := strings.SplitN(t, "\n", 2)[0]
			return []string{"the jq part prints JSON (" + first + " …), not text rows, so column can't align it: " +
				"each row must be [..] | @tsv on its own, not wrapped in another [ ]; with jq -r"}
		}
	}
	return nil
}
