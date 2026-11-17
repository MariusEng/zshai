package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJudge(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"ok.json":    `[{"a":1},{"a":2}]`,
		"lines.json": "{\"a\":1}\n{\"a\":2}\n",
		"bad.csv":    "a,b\n1,2,3\n",
		"tom.txt":    "",
		"tom.json":   "",
	}
	for name, body := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	var effs []Effect
	for name := range files {
		effs = append(effs, Effect{Kind: "created", Path: name})
	}
	c := Judge(&Result{Copy: dir, Effects: effs}, "lag filene")
	got := strings.Join(c.Problems, "\n")
	for _, want := range []string{"lines.json, but it holds 2 separate JSON values", "bad.csv, but its CSV is broken", "tom.json, but it is empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "ok.json") || strings.Contains(got, "tom.txt") {
		t.Errorf("false alarm:\n%s", got)
	}
	// blocked by the sandbox: never the command's fault
	if c := Judge(&Result{Copy: dir, Exit: 6, Stderr: "curl: (6) Could not resolve host", Denied: true}, ""); len(c.Problems) > 0 {
		t.Errorf("denied run blamed: %v", c.Problems)
	}
}

func TestPairMoves(t *testing.T) {
	effs := []Effect{{Kind: "created", Path: "arkiv", IsDir: true}, {Kind: "created", Path: "arkiv/logs", IsDir: true},
		{Kind: "deleted", Path: "logs", IsDir: true}}
	effs = append(effs, Effect{Kind: "created", Path: "arkiv/logs/a.log", Size: 2})
	got := collapse(pairMoves(effs, nil, nil))
	if len(got) != 2 || got[0].Kind != "moved" || got[0].From != "logs" || got[0].Path != "arkiv/logs" {
		t.Errorf("got %+v", got)
	}
}

func TestDirHint(t *testing.T) {
	mac := "rm: zshai-0.3.0-darwin-arm64: is a directory\nrm: zshai-0.4.0-darwin-arm64: is a directory\n"
	if h := dirHint(mac, "slett alle zshai* filer i denne mappen"); !strings.Contains(h, "(.)") || !strings.Contains(h, "zshai-0.3.0-darwin-arm64/") {
		t.Errorf("files: %q", h)
	}
	linux := "rm: cannot remove 'build': Is a directory\n"
	if h := dirHint(linux, "delete everything named build*"); !strings.Contains(h, "rm -r") {
		t.Errorf("folders too: %q", h)
	}
	if dirHint("rm: x: No such file or directory", "slett x") != "" {
		t.Error("not a folder problem")
	}
}

func TestDirHintThisFolder(t *testing.T) {
	for _, req := range []string{"slett zshai* i denne mappa", "delete the zshai files in this folder", "slett zshai-filene her"} {
		if h := dirHint("rm: a: is a directory", req); !strings.Contains(h, "(.)") {
			t.Errorf("%q: %q", req, h)
		}
	}
}
