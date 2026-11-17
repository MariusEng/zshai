package dryrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestZshGlob(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "zshai-0.4.0-darwin-arm64"), 0o755)
	os.WriteFile(filepath.Join(d, "zshai-0.4.0.zip"), nil, 0o644)
	os.WriteFile(filepath.Join(d, "zshai-0.4.0.tar.gz"), nil, 0o644)
	got, ok := zshGlob("zshai*(.)", d)
	slices.Sort(got)
	if !ok || !slices.Equal(got, []string{"zshai-0.4.0.tar.gz", "zshai-0.4.0.zip"}) {
		t.Errorf("(.): %v %v", got, ok)
	}
	if got, ok := zshGlob("zshai*(/)", d); !ok || !slices.Equal(got, []string{"zshai-0.4.0-darwin-arm64"}) {
		t.Errorf("(/): %v %v", got, ok)
	}
	// qualifiers that run code are never expanded
	for _, w := range []string{"*(e:'touch x':)", "*(+fn)", "*(P:x:)", "$(id)*(.)"} {
		if _, ok := zshGlob(w, d); ok {
			t.Errorf("%s was expanded", w)
		}
	}
	if _, err := os.Stat(filepath.Join(d, "x")); err == nil {
		t.Error("a qualifier ran code")
	}
}
