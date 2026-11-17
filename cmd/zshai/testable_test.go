package main

import (
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

// Killing processes is never test-run, however it is spelled: a test run of
// "… | xargs kill" would kill the user's real processes (the sandbox only guards files).
func TestTestableProcesses(t *testing.T) {
	for src, want := range map[string]bool{
		"ps aux | grep python | grep -v grep | awk '{print $2}' | xargs kill": false,
		"pgrep -f app | xargs -n1 kill -9":                                    false,
		"kill $(pgrep python)":                                                false,
		"pkill -f webapp":                                                     false,
		"sudo rm -rf build":                                                   false,
		"rm *.tmp":                                                            true,
		"find . -name '*.tmp' | xargs rm":                                     true,
	} {
		s, err := shell.Parse(src, nil)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got := testable(s); got != want {
			t.Errorf("testable(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestReplaceWord(t *testing.T) {
	for _, c := range [][3]string{
		{"ls | gerp x", "gerp", "ls | grep x"},
		{"gerp x; gerp y", "gerp", "grep x; gerp y"},
		{"cat gerpfile | gerp x", "gerp", "cat gerpfile | grep x"},
	} {
		if got, _ := replaceWord(c[0], c[1], "grep"); got != c[2] {
			t.Errorf("replaceWord(%q) = %q, want %q", c[0], got, c[2])
		}
	}
}
