package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestExitClosesShell(t *testing.T) {
	for cmd, bad := range map[string]bool{
		`echo "Datoen er ikke angitt." && exit 1`: true, // seen with the real zshai-mini
		`x=$(exit 3); echo $?`:                    false,
		`(cd /tmp && exit 0)`:                     false,
		`ssh host 'exit'`:                         false,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := false
		for _, p := range Check(s, Env{OS: "linux", Cwd: t.TempDir()}).Problems {
			got = got || len(p) > 4 && p[:4] == "exit"
		}
		if got != bad {
			t.Errorf("%s: flagged %v, want %v", cmd, got, bad)
		}
	}
}

func TestNewFile(t *testing.T) {
	cwd, home := t.TempDir(), "/home/u"
	os.WriteFile(filepath.Join(cwd, "sales.csv"), []byte("a,b\n"), 0o644)
	cases := []struct {
		req, cmd string
		bad      bool
	}{
		// seen on the Mac: not JSON, and in /tmp
		{"opprett en json-fil i denne mappen med 5 tilfeldige navn og epost-adresser",
			"for ((i=1; i<=5; i++)); do\n  echo \"navn: $i.email@x.de\" > /tmp/navn$i.email\ndone", true},
		{"opprett en json-fil i denne mappen med 5 tilfeldige navn og epost-adresser",
			"cat > navn.json <<'EOF'\n[{\"navn\": \"Kari\", \"epost\": \"kari@example.com\"}]\nEOF", false},
		{"create a csv file with the processes", "ps -axo pid=,comm= | awk '{print $1\",\"$2}' > processes.csv", false},
		{"create a csv file with the processes", "ps aux > /tmp/processes.csv", true},     // not here
		{"lag en fil notes.txt i ~/Documents", "echo hei > ~/Documents/notes.txt", false}, // a place was named
		{"lag en tekstfil med dagens dato", "date > dato.txt", false},
		{"lag en yaml-fil med config", "printf 'a: 1\\n' > config.json", true},
		{"vis json-fila", "jq . data.json", false},
		// seen with the real zshai-mini: printed, no file
		{"lag en csv-fil med 3 byer og innbyggertall", `echo "A,B,C" | column -t -s "," && echo "1,2,3" | column -t -s ","`, true},
		{"lag en csv-fil av rapporten", "cp rapport.csv ny.csv", false},
		// seen with the real zshai-mini: overwrites a file that is there
		{"lag en csv-fil med 3 byer og innbyggertall", `echo "1,2,3" > sales.csv`, true},
		{"skriv om sales.csv til en ny csv-fil", `(head -n 1 sales.csv; tail -n +2 sales.csv | sort) > sales-sortert.csv`, false},
		// seen with the real zshai-mini: one file per name, and not JSON
		{"opprett en json-fil i denne mappen med 5 tilfeldige navn og epost-adresser",
			"for ((i=1; i<=5; i++)); do\n  echo \"navn_${i}: x@mail.com\" > \"${i}.json\"\ndone", true}, // not creating
		{"lag en json-fil av sales.csv", "jq -R . sales.csv > /dev/null; jq -s . x > sales.json", false},
	}
	for _, c := range cases {
		s, err := shell.Parse(c.cmd, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.cmd, err)
		}
		got := NewFile(s, c.req, cwd, home)
		if (len(got) > 0) != c.bad {
			t.Errorf("%q\n%s\n got %v", c.req, c.cmd, got)
		}
	}
}

// a script written by the same command and then run is not "command not found"
func TestRunsFileItWrote(t *testing.T) {
	src := "cat > timestamp.sh <<'EOF'\n#!/bin/zsh\ndate\nEOF\nchmod +x timestamp.sh\n./timestamp.sh"
	s, err := shell.Parse(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Check(s, Env{Cwd: t.TempDir()}).Problems {
		if strings.Contains(p, "command not found") {
			t.Errorf("problem: %s", p)
		}
	}
	s, _ = shell.Parse("./nothere.sh", nil)
	if ps := Check(s, Env{Cwd: t.TempDir()}).Problems; len(ps) == 0 {
		t.Error("a script that doesn't exist should still be caught")
	}
}
