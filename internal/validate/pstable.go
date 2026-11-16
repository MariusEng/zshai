package validate

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/MariusEng/zshai/internal/shell"
)

// PowerShell-style tables (config "tables": "powershell", the default). The models
// learned to make tables with  jq -r '… @tsv' | column -t  and  column -s, -t f.csv;
// those exact recipes are rewritten to  zshai fmt -t, which prints a table the way
// PowerShell's Format-Table does (dashed header, numbers right-aligned).

var (
	columnTab = `column\s+(?:-t\s+-s\s*\$'\\t'|-s\s*\$'\\t'\s+-t|-ts\s*\$'\\t')`
	// jq [-s] -r '<pre> | (.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' FILE | column -t -s $'\t'
	jqTableRe = regexp.MustCompile(`^jq\s+((?:-[rs]+\s+)+)'([^']*)'\s+([^\s|;&<>()'"$]+)\s*\|\s*` + columnTab + `\s*$`)
	jqRecipe  = regexp.MustCompile(`\(\s*\.\[0\]\s*\|\s*keys_unsorted\s*\)\s*as\s*\$k\s*\|\s*\(\s*\$k\s*\|\s*@tsv\s*\)\s*,\s*` +
		`\(\s*\.\[\]\s*\|\s*\[\s*\.\[\$k\[\]\]\s*(?:\|\s*tostring\s*)?\]\s*\|\s*@tsv\s*\)\s*$`)
	csvOpts = `(?:-s\s*(?:,|';'|\$'\\t'|'\\t')\s+-t|-t\s+-s\s*(?:,|';'|\$'\\t'|'\\t')|-ts\s*(?:,|';'))`
	// column -s, -t [<] f.csv
	csvTableRe = regexp.MustCompile(`^column\s+` + csvOpts + `\s+(?:<\s*)?([^\s|;&<>()'"$]+)\s*$`)
	// (head -n 1 f.csv; tail -n +2 f.csv | sort -t, -k3[,3] [-nr]) | column -s, -t
	csvSortRe = regexp.MustCompile(`^\(\s*head\s+-n\s*1\s+([^\s|;&<>()'"$]+)\s*;\s*tail\s+-n\s*\+2\s+([^\s|;&<>()'"$]+)\s*\|\s*` +
		`sort\s+((?:-[a-zA-Z]+\s+)*)-t\s*(?:(.)|'(.)')\s+((?:-[a-zA-Z]+\s+)*)-k\s*(\d+)(?:,\d+)?((?:\s+-[a-zA-Z]+)*)\s*\)\s*\|\s*column\s+` +
		csvOpts + `\s*$`)
)

// PSTable returns the zshai fmt form of a column-table command, or "" if it isn't one
// of the recipes. cwd resolves the CSV file whose header names the sort column.
func PSTable(src, cwd string) string {
	src = strings.TrimSpace(src)
	if m := jqTableRe.FindStringSubmatch(src); m != nil {
		if !strings.Contains(m[1], "r") || !jqRecipe.MatchString(m[2]) {
			return ""
		}
		pre := strings.TrimSpace(jqRecipe.ReplaceAllString(m[2], ""))
		pre = strings.TrimSpace(strings.TrimSuffix(pre, "|"))
		if pre == "" || pre == "." {
			return "zshai fmt -t " + m[3] // zshai fmt reads JSON Lines too
		}
		slurp := ""
		if strings.Contains(m[1], "s") {
			slurp = "-s "
		}
		return "jq " + slurp + quote(pre) + " " + m[3] + " | zshai fmt -t"
	}
	if m := csvTableRe.FindStringSubmatch(src); m != nil {
		return "zshai fmt -t " + m[1]
	}
	if m := csvSortRe.FindStringSubmatch(src); m != nil && m[1] == m[2] {
		file := m[1]
		delim := m[4] + m[5]
		col, _ := strconv.Atoi(m[7])
		flags := m[3] + m[6] + m[8]
		name := csvHeader(filepath.Join(cwd, file), delim, col)
		if name == "" {
			return ""
		}
		key := name
		if strings.Contains(flags, "r") {
			key = "-" + name
		}
		return "zshai fmt -t --sort " + quote(key) + " " + file
	}
	return ""
}

// fmtCheck runs  zshai fmt … FILE  (not fed by a pipe) once on the real file: a file that
// isn't JSON/CSV or a bad option goes back to the model. Only zshai fmt; other zshai
// commands are not the model's to use.
func fmtCheck(c *shell.Cmd, cwd string) []string {
	if len(c.Args) == 0 || c.Args[0] != "fmt" {
		return []string{"of zshai's own commands only zshai fmt can be used in a command"}
	}
	if c.PipeFrom != nil || len(c.Redirs) > 0 || c.Remote != "" {
		return nil
	}
	for i := range c.Args {
		if !c.Lit[i] {
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "zshai" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, c.Args...)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return []string{"running it on the real files failed: " + strings.SplitN(msg, "\n", 2)[0]}
		}
	}
	return nil
}

func csvHeader(path, delim string, col int) string {
	f, err := os.Open(path)
	if err != nil || col < 1 || delim == "" {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Split(strings.TrimRight(strings.TrimPrefix(line, "\xef\xbb\xbf"), "\r\n"), delim)
	if col > len(fields) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(fields[col-1]), `"`)
}

func quote(s string) string {
	if q, err := syntax.Quote(s, syntax.LangBash); err == nil && !strings.HasPrefix(q, "$'") {
		return q
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
