package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestTryRun(t *testing.T) {
	for _, b := range []string{"jq", "zsh", "column"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip("no " + b)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p.json"), []byte("{\"pid\":1,\"user\":\"root\"}\n{\"pid\":2,\"user\":\"x\"}\n"), 0o644)
	for src, want := range map[string]string{
		`jq -r 'select(.user == "root") | [.pid, .user]' p.json | jq -r '(.[0] | keys_unsorted) as $k | $k | @tsv'`:              "has no keys",
		`jq -r '[.pid, .user] | @tsv' p.json | column -t -s $'\t'`:                                                               "",
		`jq -r '[[.pid, .user] | @tsv]' p.json | column -t -s $'\t'`:                                                             "prints JSON",
		`jq -r -s '(.[] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' p.json | column -t -s $'\t'`:  "2 times",
		`jq -r -s '(.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' p.json | column -t -s $'\t'`: "",
		`grep nothing p.json | wc -l`:           "",
		`jq -r '.pid' p.json | sort -o out.txt`: "", // writes: not run
	} {
		s, err := shell.Parse(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(TryRun(s, dir), " ")
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", src, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err == nil {
		t.Error("sort -o was run")
	}
}
