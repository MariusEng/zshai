// Package contextinfo builds the compact context the model sees (PLAN.md §2):
// OS and tool flavour, package manager, installed tools, cwd listing (v2),
// git state, Azure state (from files, no az call), ssh hosts, last command.
package contextinfo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/tools"
)

type Info struct {
	OS       string // "linux" | "darwin"
	OSName   string // "Ubuntu 26.04.1 LTS" / "macOS 26.1"
	Flavour  string // "gnu" | "bsd" (+ note)
	Zsh      string
	Manager  string
	Have     []string
	Cwd      string
	Home     string
	Listing  string
	Named    []string // files named in the request, found up to 3 levels down
	Git      *Git
	Azure    *Azure
	Hosts    []string
	Last     string
	LastExit int
	Notes    []string
	Language string
}

type Git struct {
	Root, Branch, Upstream                 string
	Ahead, Behind                          int
	Staged, Modified, Untracked, Conflicts int
	Stashes                                int
	Remotes                                []string
	Op                                     string // merge, rebase, ...
}

type Azure struct {
	LoggedIn              bool
	Subscription, SubID   string
	Tenant, User, Cloud   string
	DefGroup, DefLocation string
}

type Input struct {
	Cwd, Request, Last, Language string
	LastExit                     int
	Notes                        []string
}

func Build(in Input) *Info {
	home, _ := os.UserHomeDir()
	inf := &Info{OS: runtime.GOOS, Cwd: in.Cwd, Home: home, Last: in.Last, LastExit: in.LastExit, Notes: in.Notes, Language: in.Language}
	if inf.Cwd == "" {
		inf.Cwd, _ = os.Getwd()
	}
	inf.OSName, inf.Flavour = osName()
	inf.Zsh = os.Getenv("ZSH_VERSION")
	inf.Manager = tools.Manager("auto")
	inf.Have = tools.Installed()
	sort.Strings(inf.Have)
	inf.Listing = listing(inf.Cwd, 40, 8)
	inf.Named = findNamed(inf.Cwd, in.Request)
	inf.Git = gitState(inf.Cwd)
	if _, err := exec.LookPath("az"); err == nil {
		inf.Azure = azureState(home)
	}
	inf.Hosts = sshHosts(home)
	return inf
}

func osName() (name, flavour string) {
	if runtime.GOOS == "darwin" {
		v, _ := run(500*time.Millisecond, "", "sw_vers", "-productVersion")
		return "macOS " + strings.TrimSpace(v), "bsd (BSD sed/date/stat/find; no GNU long options)"
	}
	name = "Linux"
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
				name = strings.Trim(v, `"`)
			}
		}
	}
	flavour = "gnu"
	if t, err := filepath.EvalSymlinks("/usr/bin/ls"); err == nil && strings.Contains(t, "cargo") {
		flavour = "gnu (coreutils are uutils/rust, GNU-compatible)"
	}
	return name, flavour
}

func run(timeout time.Duration, dir string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// listing renders the cwd with one level of folder contents (Context v2).
func listing(dir string, maxTop, maxSub int) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "(unreadable)"
	}
	var parts []string
	n := 0
	for _, e := range ents {
		if n >= maxTop {
			parts = append(parts, fmt.Sprintf("… +%d more", len(ents)-n))
			break
		}
		n++
		name := e.Name()
		if e.IsDir() {
			sub, _ := os.ReadDir(filepath.Join(dir, name))
			var sn []string
			for i, s := range sub {
				if i >= maxSub {
					sn = append(sn, fmt.Sprintf("…+%d", len(sub)-i))
					break
				}
				x := s.Name()
				if s.IsDir() {
					x += "/"
				}
				sn = append(sn, x)
			}
			if len(sub) == 0 {
				parts = append(parts, name+"/ (empty)")
			} else {
				parts = append(parts, fmt.Sprintf("%s/ [%s]", name, strings.Join(sn, " ")))
			}
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			parts = append(parts, fmt.Sprintf("%s (%s, %s)", name, HumanSize(info.Size()), info.ModTime().Format("2006-01-02")))
		} else {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "; ")
}

// HumanSize formats bytes like 3.4 MB.
func HumanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// findNamed finds files whose names appear in the request (up to 3 levels down).
func findNamed(dir, req string) []string {
	var words []string
	for _, w := range strings.FieldsFunc(req, func(r rune) bool {
		return r == ' ' || r == ',' || r == '?' || r == '"' || r == '\'' || r == '«' || r == '»'
	}) {
		w = strings.TrimRight(w, ".!:;")
		if strings.ContainsAny(w, "./") && len(w) > 2 {
			words = append(words, strings.ToLower(filepath.Base(w)))
		}
	}
	if len(words) == 0 {
		return nil
	}
	var found []string
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel != "." && (strings.Count(rel, string(os.PathSeparator)) >= 3 || strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		lname := strings.ToLower(d.Name())
		for _, w := range words {
			if lname == w {
				info, _ := d.Info()
				s := rel
				if info != nil {
					s = fmt.Sprintf("%s (%s)", rel, HumanSize(info.Size()))
				}
				found = append(found, s+headLine(p))
				break
			}
		}
		if len(found) >= 5 {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// headLine returns the first line of a CSV/TSV (its header), which helps the model pick columns.
// peekable: text formats worth showing the model the first lines of
var peekExt = map[string]bool{".csv": true, ".tsv": true, ".json": true, ".jsonl": true, ".ndjson": true, ".yaml": true,
	".yml": true, ".toml": true, ".xml": true, ".log": true, ".txt": true, ".md": true, ".ini": true, ".conf": true,
	".html": true, ".sql": true, ".psv": true, ".dat": true, ".out": true}

// never shown, even to the local model
var secretName = regexp.MustCompile(`(?i)(^\.env|id_[a-z0-9]+$|\.(pem|key|p12|pfx|kdbx)$|secret|credential|passw|token)`)

// JSONShape describes a JSON file for the model: array / JSON Lines / object, how many
// items, and the keys with their types in file order.
func JSONShape(p string) string {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() > 4<<20 {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	t := bytes.TrimSpace(data)
	if len(t) == 0 {
		return ""
	}
	if json.Valid(t) {
		switch t[0] {
		case '[':
			var items []json.RawMessage
			if json.Unmarshal(t, &items) != nil {
				return ""
			}
			if len(items) > 0 && bytes.HasPrefix(bytes.TrimSpace(items[0]), []byte("{")) {
				return fmt.Sprintf("JSON array of %d objects; keys: %s", len(items), objectKeys(items[0]))
			}
			return fmt.Sprintf("JSON array of %d values", len(items))
		case '{':
			return "one JSON object; top-level keys: " + objectKeys(t)
		}
		return ""
	}
	// JSON Lines: every non-empty line is a value of its own
	var first json.RawMessage
	n := 0
	for _, l := range bytes.Split(t, []byte("\n")) {
		l = bytes.TrimSpace(l)
		if len(l) == 0 {
			continue
		}
		if !json.Valid(l) {
			return ""
		}
		if n == 0 {
			first = l
		}
		n++
	}
	if n < 2 || !bytes.HasPrefix(first, []byte("{")) {
		return ""
	}
	return fmt.Sprintf("JSON Lines: %d objects, one per line, NOT an array; keys: %s. "+
		"jq runs the filter on each line by itself: jq -r '[.%s] | @tsv' (no .[]); "+
		"to sort, count or make a table with a header use jq -s (slurp) to get one array", n, objectKeys(first), firstKey(first))
}

// objectKeys: "pid (number), user (string), …" in file order
func objectKeys(obj []byte) string {
	dec := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ""
	}
	var parts []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			break
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			break
		}
		typ := "string"
		switch b := bytes.TrimSpace(v); {
		case len(b) == 0:
		case b[0] == '{':
			typ = "object"
		case b[0] == '[':
			typ = "array"
		case b[0] == '"':
			typ = "string"
		case string(b) == "true" || string(b) == "false":
			typ = "bool"
		case string(b) == "null":
			typ = "null"
		default:
			typ = "number"
		}
		parts = append(parts, fmt.Sprintf("%v (%s)", k, typ))
		if len(parts) == 20 {
			parts = append(parts, "…")
			break
		}
	}
	return strings.Join(parts, ", ")
}

func firstKey(obj []byte) string {
	k := objectKeys(obj)
	if i := strings.Index(k, " ("); i > 0 {
		return k[:i]
	}
	return "key"
}

// headLine shows the model what is inside a named file: its format (JSON array or JSON Lines,
// CSV delimiter, …) and its first lines, so a jq/awk/column command fits the real structure.
func headLine(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	if !peekExt[ext] || secretName.MatchString(filepath.Base(p)) {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if n == 0 {
		return " (empty)"
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l) > 160 {
			l = l[:160] + "…"
		}
		lines = append(lines, l)
		if len(lines) == 3 {
			break
		}
	}
	kind := ""
	switch {
	case ext == ".json" || ext == ".jsonl" || ext == ".ndjson":
		if sh := JSONShape(p); sh != "" {
			// the shape says more than the first lines of an indented file
			first := lines[0]
			return " — " + sh + "; first line: " + first
		}
	case ext == ".csv" || ext == ".tsv" || ext == ".psv":
		h := lines[0]
		d := map[string]int{",": strings.Count(h, ","), ";": strings.Count(h, ";"), "tab": strings.Count(h, "\t"), "|": strings.Count(h, "|")}
		best, bn := "", 0
		for k, v := range d {
			if v > bn || v == bn && k == "," {
				best, bn = k, v
			}
		}
		if bn > 0 {
			kind = "delimiter " + best
		}
	}
	out := ""
	if kind != "" {
		out = " — " + kind
	}
	out += "; first lines:"
	for _, l := range lines {
		out += "\n    " + l
	}
	return out
}

func gitState(dir string) *Git {
	out, err := run(400*time.Millisecond, dir, "git", "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return nil
	}
	g := &Git{}
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "# branch.head "):
			g.Branch = strings.TrimPrefix(l, "# branch.head ")
		case strings.HasPrefix(l, "# branch.upstream "):
			g.Upstream = strings.TrimPrefix(l, "# branch.upstream ")
		case strings.HasPrefix(l, "# branch.ab "):
			f := strings.Fields(l)
			if len(f) == 4 {
				g.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
				g.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[3], "-"))
			}
		case strings.HasPrefix(l, "1 ") || strings.HasPrefix(l, "2 "):
			xy := strings.Fields(l)[1]
			if xy[0] != '.' {
				g.Staged++
			}
			if xy[1] != '.' {
				g.Modified++
			}
		case strings.HasPrefix(l, "u "):
			g.Conflicts++
		case strings.HasPrefix(l, "? "):
			g.Untracked++
		}
	}
	root, _ := run(300*time.Millisecond, dir, "git", "rev-parse", "--show-toplevel", "--git-dir")
	lines := strings.Split(strings.TrimSpace(root), "\n")
	if len(lines) > 0 {
		g.Root = lines[0]
	}
	if len(lines) > 1 {
		gd := lines[1]
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		for op, file := range map[string]string{"merge": "MERGE_HEAD", "rebase": "rebase-merge", "rebase ": "rebase-apply", "cherry-pick": "CHERRY_PICK_HEAD", "bisect": "BISECT_LOG", "revert": "REVERT_HEAD"} {
			if _, err := os.Stat(filepath.Join(gd, file)); err == nil {
				g.Op = strings.TrimSpace(op)
			}
		}
	}
	if st, err := run(300*time.Millisecond, dir, "git", "stash", "list"); err == nil && strings.TrimSpace(st) != "" {
		g.Stashes = strings.Count(strings.TrimSpace(st), "\n") + 1
	}
	if rm, err := run(300*time.Millisecond, dir, "git", "remote"); err == nil {
		g.Remotes = strings.Fields(rm)
	}
	return g
}

func azureState(home string) *Azure {
	a := &Azure{}
	b, err := os.ReadFile(filepath.Join(home, ".azure", "azureProfile.json"))
	if err == nil {
		b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // the file has a UTF-8 BOM
		var p struct {
			Subscriptions []struct {
				ID, Name, TenantID, EnvironmentName string
				IsDefault                           bool
				User                                struct{ Name string }
			}
		}
		if json.Unmarshal(b, &p) == nil {
			for _, s := range p.Subscriptions {
				if s.IsDefault {
					a.LoggedIn = true
					a.Subscription, a.SubID, a.Tenant, a.Cloud, a.User = s.Name, s.ID, s.TenantID, s.EnvironmentName, s.User.Name
				}
			}
		}
	}
	if f, err := os.Open(filepath.Join(home, ".azure", "config")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		section := ""
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(l, "[") {
				section = strings.Trim(l, "[]")
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok || section != "defaults" {
				continue
			}
			switch strings.TrimSpace(k) {
			case "group":
				a.DefGroup = strings.TrimSpace(v)
			case "location":
				a.DefLocation = strings.TrimSpace(v)
			}
		}
	}
	return a
}

// sshHosts reads host names (no addresses, users or keys) from ~/.ssh/config.
func sshHosts(home string) []string {
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var hosts []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 || !strings.EqualFold(fs[0], "host") {
			continue
		}
		for _, h := range fs[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] {
				continue
			}
			seen[h] = true
			hosts = append(hosts, h)
		}
		if len(hosts) >= 30 {
			break
		}
	}
	return hosts
}

// a question about zshai itself (its config, log, history), not e.g. "the zshai project on server"
var (
	zshaiRe    = regexp.MustCompile(`(?i)\bz(sh)?ai\b`)
	zshaiOwnRe = regexp.MustCompile(`(?i)\b(config\w*|konfig\w*|innstilling\w*|settings?|log|logg\w*|loggen|db|database\w*|journal|historikk\w*|history|forespørsl\w*|requests?|kommandoer|commands)\b`)
)

// Render produces the context block for the prompt.
func (i *Info) Render(request string) string {
	var b strings.Builder
	cwd := i.Cwd
	if i.Home != "" && strings.HasPrefix(cwd, i.Home) {
		cwd = "~" + strings.TrimPrefix(cwd, i.Home)
	}
	fmt.Fprintf(&b, "os: %s; %s\n", i.OSName, i.Flavour)
	if i.Manager != "" {
		fmt.Fprintf(&b, "packages: %s\n", i.Manager)
	} else {
		fmt.Fprintf(&b, "packages: none\n")
	}
	fmt.Fprintf(&b, "have: %s\n", strings.Join(i.Have, " "))
	fmt.Fprintf(&b, "cwd: %s\n", cwd)
	fmt.Fprintf(&b, "ls: %s\n", i.Listing)
	for _, n := range i.Named {
		fmt.Fprintf(&b, "named: %s\n", n)
	}
	if g := i.Git; g != nil {
		fmt.Fprintf(&b, "git: branch %s", g.Branch)
		if g.Upstream != "" {
			fmt.Fprintf(&b, " → %s (ahead %d, behind %d)", g.Upstream, g.Ahead, g.Behind)
		} else {
			b.WriteString(" (no upstream)")
		}
		fmt.Fprintf(&b, "; staged %d, modified %d, untracked %d", g.Staged, g.Modified, g.Untracked)
		if g.Conflicts > 0 {
			fmt.Fprintf(&b, ", conflicts %d", g.Conflicts)
		}
		if g.Stashes > 0 {
			fmt.Fprintf(&b, "; stashes %d", g.Stashes)
		}
		if len(g.Remotes) > 0 {
			fmt.Fprintf(&b, "; remotes %s", strings.Join(g.Remotes, " "))
		}
		if g.Op != "" {
			fmt.Fprintf(&b, "; %s in progress", g.Op)
		}
		b.WriteString("\n")
	}
	if a := i.Azure; a != nil {
		if a.LoggedIn {
			fmt.Fprintf(&b, "az: logged in; subscription %q", a.Subscription)
			if a.DefGroup != "" {
				fmt.Fprintf(&b, "; default group %s", a.DefGroup)
			}
			if a.DefLocation != "" {
				fmt.Fprintf(&b, "; default location %s", a.DefLocation)
			}
			b.WriteString("\n")
		} else {
			b.WriteString("az: not logged in (az login)\n")
		}
	}
	if len(i.Hosts) > 0 {
		fmt.Fprintf(&b, "hosts: %s\n", strings.Join(i.Hosts, " "))
	}
	if zshaiRe.MatchString(request) && zshaiOwnRe.MatchString(request) {
		// zshai's own files, so "vis config for zshai" doesn't get an invented path
		tilde := func(p string) string {
			if i.Home != "" && strings.HasPrefix(p, i.Home) {
				return "~" + strings.TrimPrefix(p, i.Home)
			}
			return p
		}
		fmt.Fprintf(&b, "zshai files: config %s (JSON); log %s (SQLite; its size: du -h; table turns: one row per request with started_at, request, script, risk, mode, outcome, exit_code (NULL = not run, >0 = failed), total_seconds, model_seconds; also responses, effects); undo journal %s\n",
			tilde(config.Path()), tilde(filepath.Join(config.StateDir(), "zshai.db")), tilde(filepath.Join(config.StateDir(), "journal.jsonl")))
	}
	if i.Last != "" {
		fmt.Fprintf(&b, "last: %s (exit %d)\n", i.Last, i.LastExit)
	}
	if len(i.Notes) > 0 {
		fmt.Fprintf(&b, "notes: %s\n", strings.Join(i.Notes, "; "))
	}
	lang := "English"
	if i.Language == "no" {
		lang = "Norwegian Bokmål (not Nynorsk)"
	}
	fmt.Fprintf(&b, "language: %s\n", lang)
	fmt.Fprintf(&b, "request: %s\n", request)
	return b.String()
}
