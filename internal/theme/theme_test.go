package theme

import (
	"strings"
	"testing"
)

func TestEveryThemeHasEveryRole(t *testing.T) {
	for _, th := range All() {
		for _, r := range Roles {
			if th.C[r] == "" {
				t.Errorf("theme %s: no colour for %s", th.Name, r)
			}
		}
	}
}

func TestKeys(t *testing.T) {
	th, _ := Get("classic")
	got := Keys(th, "ja / nei / vis / endre")
	if Keys(th, "Skriv JA for å kjøre") != "Skriv JA for å kjøre" {
		t.Error("Keys changed the typed-JA question")
	}
	for _, want := range []string{"\x1b[1;32mj\x1b[0ma", "\x1b[1;31mn\x1b[0mei", "\x1b[1;33mv\x1b[0mis", "\x1b[1;36me\x1b[0mndre"} {
		if !strings.Contains(got, want) {
			t.Errorf("Keys: %q lacks %q", got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	if Resolve("auto", "light").Name != "light" || Resolve("auto", "").Name != "gold" || Resolve("nord", "light").Name != "nord" {
		t.Error("Resolve picks the wrong theme")
	}
}
