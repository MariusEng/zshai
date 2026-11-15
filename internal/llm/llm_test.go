package llm

import (
	"strings"
	"testing"
)

func TestParseRunaway(t *testing.T) {
	raw := "r: read\nm: show\na:\nn:\nx: 0\ne: lsof -i :8080 viser prosessen.\n---\nlsof -i :8080\ne: Med -t får du bare PID: lsof -ti :8080\n---\nlsof -ti :8080\ne: mer\n---\nlsof"
	a, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Script != "lsof -i :8080" {
		t.Errorf("script = %q", a.Script)
	}
	if len(a.Explain) != 2 || !a.Show {
		t.Errorf("explain = %q show=%v", a.Explain, a.Show)
	}
}

func TestParseMultilineCommand(t *testing.T) {
	a, _ := Parse("r: read\nm: show\na:\nn:\nx: 0\ne: counts lines\n---\nfor f in *.txt; do\n  wc -l \"$f\"\ndone")
	if strings.Count(a.Script, "\n") != 2 {
		t.Errorf("script = %q", a.Script)
	}
}

func TestParseRepeatedCommandInExplain(t *testing.T) {
	a, _ := Parse("r: read\nm: show\na:\nn:\nx: 0\ne: ls -d */\ne: lists folders\n---\nls -d */")
	if len(a.Explain) != 1 || a.Explain[0] != "lists folders" {
		t.Errorf("explain = %q", a.Explain)
	}
}

func TestWantsShow(t *testing.T) {
	for q, want := range map[string]bool{
		"how do I list directories?": true, "hvordan lister jeg mapper": true, "forklar tar -xzf a": true,
		"explain git rebase": true, "what does chmod 755 do": true, "hva gjør rsync -a": true,
		"list directories": false, "slett loggfilene": false, "hvor mange filer er det her?": false,
	} {
		if WantsShow(q) != want {
			t.Errorf("%q: want %v", q, want)
		}
	}
}

func TestWantsShowCommandPhrasing(t *testing.T) {
	for _, r := range []string{"hvilken kommando viser ledig minne", "what's the command to list ports",
		"vis meg kommandoen for å pakke ut zip", "hvordan pakker jeg ut tar"} {
		if !WantsShow(r) {
			t.Errorf("WantsShow(%q) = false", r)
		}
	}
	for _, r := range []string{"how long has this machine been running", "vis meg config for zshai", "hvor mye minne er ledig"} {
		if WantsShow(r) {
			t.Errorf("WantsShow(%q) = true", r)
		}
	}
}
