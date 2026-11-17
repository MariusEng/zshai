package sandbox

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MariusEng/zshai/internal/psfmt"
)

// Check is what a test run says about the command.
type Check struct {
	Problems []string // for the model: the command didn't do the job
	Denied   bool     // it reached outside the copy (network, other files, system services)
	TimedOut bool     // it ran out of time
	DirsHit  bool     // rm hit folders while the user asked for files (validate.FilesOnly can fix it)
	Files    []FileNote
}

// FileNote: an effect, with what a created or changed file holds (for the user).
type FileNote struct {
	Effect
	Items  int    // JSON array length / CSV rows / lines; -1 unknown
	Format string // json | jsonl | csv | text | ""
	Valid  bool   // json/jsonl/csv: it parsed
}

var emptyRe = regexp.MustCompile(`(?i)\b(tom|tomme|empty|blank|touch)\b`)

// Judge reads a test run. Failures with a clear cause go back to the model; a run that
// hit the sandbox's walls or the time limit is only "partly tested", never a failure.
func Judge(r *Result, request string) Check {
	c := Check{Denied: r.Denied, TimedOut: r.TimedOut}
	partly := r.Denied || r.TimedOut
	wantsEmpty := emptyRe.MatchString(request)
	if !partly && r.Exit != 0 && strings.TrimSpace(r.Stderr) != "" {
		p := fmt.Sprintf("a test run in a copy of the folder failed (exit %d): %s", r.Exit, firstLines(r.Stderr, 3))
		if h := dirHint(r.Stderr, request); h != "" {
			p += "; " + h
			c.DirsHit = !foldersAsked.MatchString(request)
		}
		c.Problems = append(c.Problems, p)
	}
	for _, e := range r.Effects {
		n := FileNote{Effect: e, Items: -1}
		if (e.Kind == "created" || e.Kind == "modified") && !e.IsDir {
			p := fileProblem(&n, filepath.Join(r.Copy, e.Path))
			if p != "" && !partly && !(wantsEmpty && p == "it is empty") {
				c.Problems = append(c.Problems, "the test run "+e.Kind+" "+e.Path+", but "+p)
			}
		}
		c.Files = append(c.Files, n)
	}
	return c
}

// fileProblem checks what a created/changed file holds, by its extension.
func fileProblem(n *FileNote, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	if len(bytes.TrimSpace(b)) == 0 {
		// data files are meant to hold something; an empty text file may be the point (touch)
		switch ext {
		case ".json", ".jsonl", ".ndjson", ".csv", ".tsv":
			return "it is empty"
		}
		return ""
	}
	switch ext {
	case ".json":
		n.Format = "json"
		v, err := psfmt.ParseJSON(b)
		if err != nil {
			return "it is not valid JSON (" + err.Error() + ")"
		}
		if multi, ok := v.([]any); ok && !bytes.HasPrefix(bytes.TrimSpace(psfmt.Text(b)), []byte("[")) {
			return fmt.Sprintf("it holds %d separate JSON values, not one JSON document: wrap them in [ … ] (or name it .jsonl)", len(multi))
		}
		n.Items, n.Valid = 1, true
		if arr, ok := v.([]any); ok {
			n.Items = len(arr)
		}
	case ".jsonl", ".ndjson":
		n.Format = "jsonl"
		v, err := psfmt.ParseJSON(b)
		if err != nil {
			return "it is not valid JSON Lines (" + err.Error() + ")"
		}
		n.Items, n.Valid = 1, true
		if arr, ok := v.([]any); ok {
			n.Items = len(arr)
		}
	case ".csv", ".tsv":
		n.Format = "csv"
		text := psfmt.Text(b)
		r := csv.NewReader(bytes.NewReader(text))
		if ext == ".tsv" {
			r.Comma = '\t'
		} else if first, _, _ := bytes.Cut(text, []byte("\n")); bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
			r.Comma = ';'
		}
		rows, err := r.ReadAll()
		if err != nil {
			return "its CSV is broken (" + err.Error() + "): every row needs the same number of fields as the header"
		}
		n.Items, n.Valid = max(0, len(rows)-1), true
	default:
		n.Format = "text"
		n.Items = bytes.Count(b, []byte("\n"))
		if !bytes.HasSuffix(b, []byte("\n")) {
			n.Items++
		}
	}
	return ""
}

// rm: zshai-0.3.0: is a directory (macOS) / rm: cannot remove 'x': Is a directory (Linux)
var isDirRe = regexp.MustCompile(`(?m)^rm: (?:cannot remove '([^']+)'|([^:]+)): [Ii]s a directory`)
var foldersAsked = regexp.MustCompile(`(?i)\b(mapper|mappene|folders|directories|alt|alle ting|everything)\b`)

// dirHint: what to write when a glob for files also matched folders.
func dirHint(stderr, request string) string {
	ms := isDirRe.FindAllStringSubmatch(stderr, 4)
	if len(ms) == 0 {
		return ""
	}
	var names []string
	for _, m := range ms {
		names = append(names, m[1]+m[2]+"/")
	}
	if foldersAsked.MatchString(request) {
		return "the pattern also matches folders (" + strings.Join(names, ", ") + "), and the user wants those gone too: use rm -r"
	}
	return "the pattern also matches folders (" + strings.Join(names, ", ") + "); the user asked for files: delete only plain files " +
		"with zsh's (.) qualifier, e.g. rm -- zshai*(.), and no -f"
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}
