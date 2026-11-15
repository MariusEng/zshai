package validate

import (
	"regexp"
	"strings"

	"github.com/MariusEng/zshai/internal/shell"
)

// Commands whose whole output is already a short, readable answer.
var summaryCmds = map[string]string{
	"uptime":  "uptime",
	"df":      "df -h ~ (or df -h <folder>)",
	"free":    "free -h",
	"sw_vers": "sw_vers",
	"vm_stat": "vm_stat",
	"w":       "w",
}

var cutters = map[string]bool{"awk": true, "cut": true, "grep": true, "head": true, "tail": true, "sed": true, "tr": true}

// the user asked for just a part ("bare tallet", "only the uptime in days", "hvor mange")
var wantsPart = regexp.MustCompile(`(?i)\b(bare|kun|only|just|antall|how many|hvor mange|count|tallet|the number|i prosent|percent|prosent)\b`)

// Trimmed finds  uptime | awk '{print $3}'  and the like: the model cut a readable answer
// down to a fragment ("3" or "646Gi") the user can't interpret. It is a soft problem: zshai
// sends it back once, and accepts the command if the model keeps it.
func Trimmed(s *shell.Script, request string) []string {
	if wantsPart.MatchString(request) {
		return nil
	}
	for _, c := range s.Cmds {
		if !cutters[c.Name] || c.PipeFrom == nil {
			continue
		}
		left, err := shell.Parse(shell.Print(c.PipeFrom), nil)
		if err != nil || len(left.Cmds) == 0 {
			continue
		}
		first := left.Cmds[0].Name
		if first == "ssh" {
			for _, rc := range left.Cmds[1:] { // [0] is ssh itself
				if rc.Remote != "" {
					first = rc.Name
					break
				}
			}
		}
		if full, ok := summaryCmds[first]; ok {
			return []string{first + " already prints a short, readable answer; show its whole output (" + full +
				") instead of cutting it down with " + c.Name + ": a bare value is hard to read"}
		}
	}
	return nil
}

// LsTotal finds  ls -lt | head -n 3: ls -l starts with a "total" line, so that shows 2 files.
func LsTotal(s *shell.Script) []string {
	for _, c := range s.Cmds {
		if c.Name != "head" || c.PipeFrom == nil {
			continue
		}
		left, err := shell.Parse(shell.Print(c.PipeFrom), nil)
		if err != nil || len(left.Cmds) != 1 || left.Cmds[0].Name != "ls" {
			continue
		}
		long, dirArg := false, false
		for i, a := range left.Cmds[0].Args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
				long = long || strings.ContainsRune(a, 'l')
				dirArg = dirArg || strings.ContainsRune(a, 'd')
			} else if left.Cmds[0].Lit[i] && a != "" && !strings.HasPrefix(a, "-") {
				dirArg = true // ls -l file.txt: no total line for a single file
			}
		}
		if long && !dirArg {
			return []string{`ls -l prints a "total" line first, so head -n N shows N-1 entries; use sed -n 2,N+1p (e.g. ls -lt | sed -n 2,4p for 3)`}
		}
	}
	return nil
}

var yesNoWord = regexp.MustCompile(`(?i)(["']|\becho\s+)(ja|nei|yes|no)\b`)

// YesNo finds a computed yes/no answer (brew outdated | wc -l | awk '{print ($1>0 ? "Ja" : "Nei")}'):
// the user can't see which things made it "yes". Printing the things themselves answers it,
// and an empty output means no.
func YesNo(s *shell.Script) []string {
	if len(yesNoWord.FindAllString(s.Src, -1)) >= 2 {
		return []string{`don't compute a yes/no answer; print the things themselves (e.g. brew outdated), an empty output means no`}
	}
	return nil
}

// RemotePath finds a remote command with a home-folder path written out
// (ssh server 'du -sh /Users/alice/git/x'): that is this machine's home, or a guess at the
// other one's. ~/… is right on the other host.
var homePath = regexp.MustCompile(`^/(Users|home)/[^/]+(/|$)`)

func RemotePath(s *shell.Script, home, cwd string) []string {
	// this machine's current folder as ~/…, unless it is the home folder itself
	here := ""
	if home != "" && home != "/" && strings.HasPrefix(cwd, home+"/") {
		here = "~/" + strings.TrimPrefix(cwd, home+"/")
	}
	for _, c := range s.Cmds {
		if c.Remote == "" {
			continue
		}
		for _, a := range c.Args {
			if here != "" && (a == here || strings.HasPrefix(a, here+"/")) {
				return []string{"this runs on " + c.Remote + ", but " + here + " is the current folder on this machine; " +
					"it says nothing about " + c.Remote + ". If you don't know where it is there, find it: ssh " + c.Remote +
					" 'find ~ -maxdepth 4 -type d -name NAME'"}
			}
			rest := ""
			switch {
			case home != "" && home != "/" && (a == home || strings.HasPrefix(a, home+"/")):
				rest = strings.TrimPrefix(strings.TrimPrefix(a, home), "/")
			case homePath.MatchString(a):
				rest = strings.TrimPrefix(a[len(homePath.FindString(a)):], "/")
			default:
				continue
			}
			return []string{"this runs on " + c.Remote + ": write ~/" + rest + " there, not the home path " + a +
				" (the home folder there may differ); if you don't know where it is, find it there"}
		}
	}
	return nil
}

var quoted = regexp.MustCompile(`"([^"]{1,40})"|«([^»]{1,40})»|“([^”]{1,40})”`)

// QuotedNames: a name the user put in quotes ("zshai") must be in the command, not swapped
// for something from the context such as the current folder's name.
func QuotedNames(s *shell.Script, request string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(request, -1) {
		name := strings.TrimSpace(m[1] + m[2] + m[3])
		if name == "" || strings.Contains(name, " ") {
			continue
		}
		// as a whole word or path part: "zshai" in ~/zshai-test doesn't count
		re := regexp.MustCompile(`(?i)(^|[^\pL\pN_.-])` + regexp.QuoteMeta(name) + `($|[^\pL\pN_-])`)
		if !re.MatchString(s.Src) {
			out = append(out, `the request names "`+name+`"; use that name in the command`)
		}
	}
	return out
}

// SortAfterTable finds  jq … @tsv | sort  and  column -t | sort: the header is sorted in with
// the rows, and after column there are no tabs left to sort on. Sort inside jq (sort_by), or
// keep the header first for CSV.
func SortAfterTable(s *shell.Script) []string {
	table := false
	for _, c := range s.Cmds {
		if c.Remote != "" {
			continue
		}
		switch {
		case c.Name == "column" && c.PipeFrom != nil, c.Name == "jq" && strings.Contains(strings.Join(c.Args, " "), "@tsv"):
			table = true
		case c.Name == "sort" && table && c.PipeFrom != nil:
			return []string{"don't sort a finished table (the header gets sorted in, and column's output has no tabs): " +
				"sort inside jq with sort_by(...) before building the rows (jq -s for JSON Lines), " +
				"or for CSV keep the header first: (head -n 1 f; tail -n +2 f | sort …) | column …"}
		}
	}
	return nil
}

var keepsHeader = regexp.MustCompile(`tail\s+-n\s*\+2|tail\s+\+2|NR\s*>\s*1|NR\s*==\s*1|FNR\s*>\s*1|sed\s+(-n\s+)?'?1d|head\s+-n\s*1\b|head\s+-1\b|--header`)

// CSVSortHeader (soft): sorting a CSV/TSV without setting its header line aside sorts the
// header in with the rows (it ends up last with sort -r/-n).
func CSVSortHeader(s *shell.Script) []string {
	csv, sorts := false, false
	for _, c := range s.Cmds {
		if c.Name == "sort" {
			sorts = true
		}
		for _, a := range c.Args {
			if l := strings.ToLower(a); strings.HasSuffix(l, ".csv") || strings.HasSuffix(l, ".tsv") {
				csv = true
			}
		}
		for _, r := range c.Redirs {
			if l := strings.ToLower(r.Target); strings.HasSuffix(l, ".csv") || strings.HasSuffix(l, ".tsv") {
				csv = true
			}
		}
	}
	if csv && sorts && !keepsHeader.MatchString(s.Src) {
		return []string{"the header line gets sorted in with the rows; keep it first: " +
			"(head -n 1 f.csv; tail -n +2 f.csv | sort -t, -k<column> …) | column -s, -t"}
	}
	return nil
}

var tableAsked = regexp.MustCompile(`(?i)\b(tabell\w*|tables?|kolonner|columns)\b`)

// TableAsked (soft): the user asked for a table, but nothing in the command lines up columns.
func TableAsked(s *shell.Script, request string) []string {
	if !tableAsked.MatchString(request) {
		return nil
	}
	for _, c := range s.Cmds {
		switch c.Name {
		case "column", "printf", "csvlook", "mlr", "xsv", "qsv", "visidata", "vd":
			return nil
		}
	}
	if strings.Contains(s.Src, "printf") || strings.Contains(s.Src, "@tsv") && strings.Contains(s.Src, "column") {
		return nil
	}
	return []string{"the user asked for a table, but this prints no aligned columns: make rows (jq -r … | @tsv, or the CSV itself) " +
		"and pipe them into column -t -s $'\\t' (CSV: column -s, -t), with a header row"}
}
