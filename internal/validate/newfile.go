package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MariusEng/zshai/internal/shell"
)

// Requests that create a file: the file has to be of the kind asked for, and in the
// folder asked for. Found on the Mac: "opprett en json-fil i denne mappen med 5 tilfeldige
// navn og epost-adresser" got a loop writing /tmp/navn$i.email: no JSON, not here.

var (
	// "json-fil", "JSON file", "a .json", "fil … i json-format"
	fileKindRe = regexp.MustCompile(`(?i)\b(json|jsonl|csv|tsv|ya?ml|txt|tekst|text|markdown|md|html|xml|toml|ini|sql)[- ]?(fil|file|filer|files|fila|filen)\b|` +
		`(?i)\.(json|jsonl|csv|tsv|ya?ml|txt|md|html|xml|toml|ini|sql)\b|(?i)\b(json|csv|yaml)[- ]format`)
	createRe = regexp.MustCompile(`(?i)\b(opprett|lag|lage|skriv|generer|create|make|write|generate|save|lagre)\b`)
	hereRe   = regexp.MustCompile(`(?i)\b(denne mapp(en|a)|mappen her|her|this (folder|directory|dir)|here|current (folder|directory))\b`)
	// the request names a place: a path, ~, or "i <folder>/på <host>" style words with a slash
	placeRe = regexp.MustCompile(`[~/]`)
)

// printers: commands that only print (to stdout), never create files themselves.
var printers = map[string]bool{"echo": true, "printf": true, "print": true, "cat": true, "column": true, "jq": true,
	"awk": true, "sed": true, "tr": true, "seq": true, "sort": true, "head": true, "tail": true, "cut": true,
	"paste": true, "nl": true, "fold": true, "shuf": true, "date": true, "jot": true, "uniq": true}

var kindExt = map[string][]string{
	"json": {".json"}, "jsonl": {".jsonl", ".ndjson", ".json"}, "csv": {".csv"}, "tsv": {".tsv", ".tab"},
	"yaml": {".yaml", ".yml"}, "yml": {".yml", ".yaml"}, "txt": {".txt"}, "tekst": {".txt"}, "text": {".txt"},
	"markdown": {".md", ".markdown"}, "md": {".md", ".markdown"}, "html": {".html", ".htm"}, "xml": {".xml"},
	"toml": {".toml"}, "ini": {".ini"}, "sql": {".sql"},
}

// written: the files the command writes with > / >> / tee (as written, maybe with $vars).
func written(s *shell.Script) []string {
	var out []string
	for _, c := range s.Cmds {
		if c.Remote != "" {
			continue
		}
		for _, r := range c.Redirs {
			if (strings.HasSuffix(r.Op, ">") || strings.HasSuffix(r.Op, ">>") || r.Op == ">|" || r.Op == "&>") && r.Raw != "" {
				if t := strings.Trim(r.Raw, `"'`); !strings.HasPrefix(t, "/dev/") && !strings.HasPrefix(t, "&") {
					out = append(out, t)
				}
			}
		}
		if c.Name == "tee" {
			for i, a := range c.Args {
				if c.Lit[i] && a != "" && !strings.HasPrefix(a, "-") {
					out = append(out, a)
				}
			}
		}
	}
	return out
}

// NewFile checks a command for a request that creates a file.
func NewFile(s *shell.Script, request, cwd, home string) []string {
	if !createRe.MatchString(request) {
		return nil
	}
	files := written(s)
	m := fileKindRe.FindStringSubmatch(request)
	if len(files) == 0 {
		// "lag en csv-fil med …" answered with echo … | column: printed, no file. Only when
		// every command just prints (cp, curl -o, tar … create files their own way).
		if m == nil || strings.Contains(request, "?") {
			return nil
		}
		for _, c := range s.Cmds {
			if !printers[c.Name] {
				return nil
			}
		}
		kind := strings.ToLower(m[1] + m[3] + m[4])
		ext := ".txt"
		if e := kindExt[kind]; len(e) > 0 {
			ext = e[0]
		}
		return []string{fmt.Sprintf("the user asked to create a %s file, but this only prints and writes no file: "+
			"write the content into one file in the cwd, e.g. cat > data%s <<'EOF' (the content) EOF", strings.ToUpper(kind), ext)}
	}
	var problems []string
	if m != nil {
		kind := strings.ToLower(m[1] + m[3] + m[4])
		exts := kindExt[kind]
		ok := false
		for _, f := range files {
			for _, e := range exts {
				if strings.HasSuffix(strings.ToLower(f), e) {
					ok = true
				}
			}
		}
		perItem := ""
		for _, f := range files {
			if strings.ContainsAny(f, "$`") {
				perItem = f
			}
		}
		if ok && perItem != "" {
			problems = append(problems, fmt.Sprintf("this writes one file per item (%s); the user asked for one %s file: "+
				"write ONE file with a fixed name and all the content in it, e.g. cat > data%s <<'EOF' (the %s content) EOF",
				perItem, strings.ToUpper(kind), exts[0], strings.ToUpper(kind)))
		}
		if !ok && len(exts) > 0 {
			name := "data" + exts[0]
			problems = append(problems, fmt.Sprintf("the user asked for one %s file, but this writes %s: write ONE file named *%s "+
				"with the content written out in full, e.g. cat > %s <<'EOF' (the %s content) EOF. "+
				"Made-up sample data (names, emails) is fine when asked for; write it out, don't compute it",
				strings.ToUpper(kind), strings.Join(files, ", "), exts[0], name, strings.ToUpper(kind)))
		}
	}
	// "lag en csv-fil med …" answered with  > sales.csv  over a file that is already there
	for _, f := range files {
		if strings.ContainsAny(f, "$`*") || strings.Contains(request, filepath.Base(f)) {
			continue
		}
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			problems = append(problems, fmt.Sprintf("%s already exists and the user asked to create a new file: "+
				"pick a new name that fits the content (not one from ls:)", f))
			break
		}
	}
	here := hereRe.MatchString(request)
	if here || !placeRe.MatchString(request) {
		for _, f := range files {
			p := f
			if strings.HasPrefix(p, "~/") || p == "~" {
				p = filepath.Join(home, strings.TrimPrefix(p, "~"))
			} else if strings.HasPrefix(p, "$HOME") {
				p = filepath.Join(home, strings.TrimPrefix(p, "$HOME"))
			}
			if !filepath.IsAbs(p) {
				continue
			}
			if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
				continue
			}
			if !here && strings.HasPrefix(p, home+"/") {
				continue // somewhere in the home folder: maybe meant, the user didn't say where
			}
			problems = append(problems, fmt.Sprintf("this writes %s, outside the folder the user is in (%s): "+
				"create the file here, with a relative name (data.json, not /tmp/…)", f, cwd))
			break
		}
	}
	return problems
}
