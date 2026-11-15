package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestJqCheck(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p.json"), []byte("{\"pid\":1,\"cmd\":\"a\"}\n{\"pid\":2,\"cmd\":\"b\"}\n"), 0o644)
	for src, want := range map[string]string{
		`jq -r '.[0] | keys_unsorted' p.json`:                "Cannot index object with number",
		`jq -s -r '.[0] | keys_unsorted | @tsv' p.json`:      "",
		`jq -r '[.pid, .cmd] | @tsv' p.json`:                 "",
		`jq '.[' p.json`:                                     "error",
		`jq -s 'sort_by(.pid) | .[] | [.pid] | @tsv' p.json`: "need -r",
		`jq -sr '.[] | [.pid] | @tsv' p.json`:                "",
		`cat p.json | jq '.[0]'`:                             "", // stdin: not tried
	} {
		s, err := shell.Parse(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range s.Cmds {
			got = append(got, jqCheck(c, dir)...)
		}
		g := strings.Join(got, " ")
		if want == "" && g != "" || want != "" && !strings.Contains(g, want) {
			t.Errorf("%s: got %q, want %q", src, g, want)
		}
	}
}

func TestJqToColumnAndCompact(t *testing.T) {
	bad, _ := shell.Parse(`jq -r 'select(.user == "root") | [.pid, .user]' p.json | column -t -s $'\t'`, nil)
	if len(jqToColumn(bad)) == 0 {
		t.Error("jqToColumn missed JSON arrays into column")
	}
	good, _ := shell.Parse(`jq -r '[.pid, .user] | @tsv' p.json | column -t -s $'\t'`, nil)
	if p := jqToColumn(good); len(p) > 0 {
		t.Errorf("jqToColumn false positive: %v", p)
	}
	c, _ := shell.Parse(`jq -c 'sort_by(.age)' users.json`, nil)
	if len(JqCompact(c, "vis users.json sortert etter alder")) == 0 {
		t.Error("JqCompact missed -c")
	}
	if len(JqCompact(c, "vis users.json kompakt")) > 0 {
		t.Error("JqCompact: compact was asked for")
	}
	piped, _ := shell.Parse(`jq -c '.[]' users.json | wc -l`, nil)
	if len(JqCompact(piped, "tell brukerne")) > 0 {
		t.Error("JqCompact: -c into another program is fine")
	}
}
