package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/shell"
)

// jq options followed by 1 or 2 values
var jqValues = map[string]int{"--arg": 2, "--argjson": 2, "--slurpfile": 2, "--rawfile": 2, "--indent": 1, "-L": 1}

// jqCheck runs a jq filter on the real input files and reports jq's errors
// ("Cannot index object with number" when the model took JSON Lines for an array).
// jq can't write files, so this is side-effect free; stdout is thrown away.
func jqCheck(c *shell.Cmd, cwd string) []string {
	if c.Name != "jq" {
		return nil
	}
	if p := jqRaw(c); p != "" {
		return []string{p}
	}
	var opts, files []string
	filter, haveFilter := "", false
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if !c.Lit[i] {
			return nil
		}
		switch {
		case a == "-f" || a == "--from-file" || a == "--args" || a == "--jsonargs" || a == "--seq":
			return nil
		case jqValues[a] > 0:
			n := jqValues[a]
			if i+n >= len(c.Args) {
				return nil
			}
			for k := 0; k <= n; k++ {
				if !c.Lit[i+k] {
					return nil
				}
			}
			opts = append(opts, c.Args[i:i+n+1]...)
			i += n
		case strings.HasPrefix(a, "-") && a != "-" && !haveFilter || strings.HasPrefix(a, "--") && haveFilter:
			opts = append(opts, a)
		case !haveFilter:
			filter, haveFilter = a, true
		default:
			files = append(files, a)
		}
	}
	if !haveFilter || len(files) == 0 {
		return nil // reads a pipe: nothing to try it on
	}
	var total int64
	for i, f := range files {
		if strings.HasPrefix(f, "~/") {
			home, _ := os.UserHomeDir()
			f = filepath.Join(home, f[2:])
		} else if !filepath.IsAbs(f) {
			f = filepath.Join(cwd, f)
		}
		fi, err := os.Stat(f)
		if err != nil || !fi.Mode().IsRegular() {
			return nil // a missing file is reported elsewhere
		}
		total += fi.Size()
		files[i] = f
	}
	if total > 50<<20 {
		return nil
	}
	bin, err := exec.LookPath("jq")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args := append(append(append([]string{}, opts...), "--", filter), files...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() == nil || ctx.Err() != nil {
		return nil
	}
	var msgs []string
	seen := map[string]bool{}
	for _, l := range strings.Split(stderr.String(), "\n") {
		l = strings.TrimSpace(l)
		// "jq: error (at f:3): X" repeats per input; keep the distinct messages
		key := l
		if i := strings.Index(l, "): "); strings.HasPrefix(l, "jq: error (at") && i > 0 {
			key = l[i+3:]
		}
		if l == "" || seen[key] {
			continue
		}
		seen[key] = true
		msgs = append(msgs, l)
		if len(msgs) == 3 {
			break
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	why := ""
	slurp := false
	for _, o := range opts {
		if o == "-s" || o == "--slurp" || strings.HasPrefix(o, "-") && !strings.HasPrefix(o, "--") && strings.Contains(o, "s") && len(o) < 5 {
			slurp = true
		}
	}
	for _, f := range files {
		if slurp && jsonArray(f) {
			why = "; " + filepath.Base(f) + " is already one JSON array: drop -s (with -s jq wraps it in another array)"
			break
		}
		if jsonLines(f) && !slurp {
			why = "; " + filepath.Base(f) + " is JSON Lines (one object per line, not an array): jq sees each object on its own, " +
				"so use .field directly without .[] / .[0], or jq -s to read all lines as one array"
			break
		}
	}
	if strings.Contains(filter, "@tsv") && strings.Contains(filter, "keys") {
		// a table attempt: give the filter that works, to copy as it is
		sl := ""
		for _, f := range files {
			if jsonLines(f) {
				sl = " -s"
			}
		}
		why += "; the table filter that works is exactly: jq -r" + sl +
			" '(.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' FILE | column -t -s $'\\t'" +
			" (sort/filter first with sort_by(...) / map(select(...)) | before it)"
	}
	return []string{"jq failed on the real file: " + strings.Join(msgs, " | ") + why}
}

// jsonLines: the first two non-empty lines are each a whole JSON object
func jsonLines(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	var objs int
	for _, l := range bytes.Split(buf[:n], []byte("\n")) {
		l = bytes.TrimSpace(l)
		if len(l) == 0 {
			continue
		}
		if !bytes.HasPrefix(l, []byte("{")) || !json.Valid(l) {
			return false
		}
		if objs++; objs == 2 {
			return true
		}
	}
	return false
}

// jqRaw: @tsv/@csv/@text rows need -r, or jq prints them as quoted JSON strings with \t in them.
func jqRaw(c *shell.Cmd) string {
	raw, formats := false, false
	for i, a := range c.Args {
		switch {
		case a == "-r" || a == "--raw-output" || a == "-j" || a == "--join-output" || a == "--raw-output0":
			raw = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rj") && c.Lit[i] && len(a) < 6:
			raw = true // -sr, -rc
		case strings.Contains(a, "@tsv") || strings.Contains(a, "@csv"):
			formats = true
		}
	}
	if formats && !raw {
		return "jq: @tsv/@csv rows need -r (raw output); without it jq prints each row as a quoted JSON string"
	}
	return ""
}

// jqToColumn: column needs text rows; jq printing JSON (arrays, objects) into column gives
// a mess of brackets. Rows come from @tsv/@csv/join/"\(…)" with -r.
func jqToColumn(s *shell.Script) []string {
	for _, c := range s.Cmds {
		if c.Name != "column" || c.PipeFrom == nil {
			continue
		}
		left, err := shell.Parse(shell.Print(c.PipeFrom), nil)
		if err != nil || len(left.Cmds) == 0 {
			continue
		}
		j := left.Cmds[len(left.Cmds)-1]
		if j.Name != "jq" {
			continue
		}
		all := strings.Join(j.Args, " ")
		if !(strings.Contains(all, "@tsv") || strings.Contains(all, "@csv") || strings.Contains(all, "join(") || strings.Contains(all, "\\(")) {
			msg := `jq prints JSON here, not text rows, so column can't make a table: end the filter with ` +
				`[...] | @tsv, use jq -r, and column -t -s $'\t'`
			// the filter already builds an array per row: spell out the fixed command
			for i, a := range j.Args {
				if !j.Lit[i] || strings.HasPrefix(a, "-") {
					continue
				}
				if t := strings.TrimSpace(a); strings.HasSuffix(t, "]") {
					fixed := strings.Replace(s.Src, a, t+" | @tsv", 1)
					if !strings.Contains(all, "-r") {
						fixed = strings.Replace(fixed, "jq ", "jq -r ", 1)
					}
					if c.Name == "column" && !strings.Contains(shell.Print(c.Stmt), " -s") {
						fixed = strings.Replace(fixed, "column -t", "column -t -s $'\\t'", 1)
					}
					msg += "; i.e.: " + fixed
				}
				break
			}
			return []string{msg}
		}
	}
	return nil
}

var wantsCompact = regexp.MustCompile(`(?i)\b(kompakt\w*|compact|én linje|en linje|one line|single line|minif\w*)\b`)

// JqCompact (soft): jq -c squeezes everything onto one line; only when the user asked for that.
func JqCompact(s *shell.Script, request string) []string {
	if wantsCompact.MatchString(request) {
		return nil
	}
	for i, c := range s.Cmds {
		if c.Name != "jq" {
			continue
		}
		// jq -c feeding another program is fine (one object per line for jq -s, wc -l, …)
		if i+1 < len(s.Cmds) && s.Cmds[i+1].PipeFrom != nil {
			continue
		}
		for _, a := range c.Args {
			if a == "-c" || a == "--compact-output" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && len(a) < 5 {
				return []string{"jq -c puts all the output on one line; the user wants to read it: drop -c"}
			}
		}
	}
	return nil
}

// jsonArray: the file starts with [
func jsonArray(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return bytes.HasPrefix(bytes.TrimSpace(buf[:n]), []byte("["))
}
