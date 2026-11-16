package prompt

import (
	"strings"
	"testing"
)

func TestRenderDefault(t *testing.T) {
	v := Values{"os": "macOS 27.2", "arch": "arm64", "flavour": "bsd", "shell": "zsh 5.9", "packages": "brew",
		"language": "English", "user": "alice", "host": "mac", "home": "~"}
	got := Render(Default, "CORE RULES", v)
	for _, want := range []string{"CORE RULES", "OS: macOS 27.2 on arm64 (bsd)", "packages: brew", "in English", "User: alice on mac"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#") || strings.Contains(got, "{") {
		t.Errorf("comments or placeholders left:\n%s", got)
	}
	if !strings.HasPrefix(got, "CORE RULES") {
		t.Errorf("{zshai} should come first:\n%s", got)
	}
}

func TestProblems(t *testing.T) {
	if p := Problems(Default); len(p) != 0 {
		t.Errorf("default template has problems: %v", p)
	}
	if p := Problems("{zshai}\nprefer {tool}\n"); len(p) != 1 || !strings.Contains(p[0], "{tool}") {
		t.Errorf("unknown placeholder not reported: %v", p)
	}
	if p := Problems("Just be helpful.\n"); len(p) != 1 || !strings.Contains(p[0], "answer format") {
		t.Errorf("replacement without the format not reported: %v", p)
	}
	if p := Problems("# {zshai} only in a comment\nreply r:/m:\n---\n"); len(p) != 0 {
		t.Errorf("custom prompt with a format reported: %v", p)
	}
}
