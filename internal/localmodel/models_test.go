package localmodel

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstalledAndPick(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ZSHAI_DATA_DIR", d)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("ZSHAI_CONFIG_DIR", cfg)
	os.MkdirAll(filepath.Join(d, "models"), 0o755)
	for _, f := range []string{"zshai-mini-0.5b-v2-Q4_K_M.gguf", "zshai-mini-1.5b-v3-Q4_K_M.gguf",
		"zshai-mini-3b-v3-Q4_K_M-00001-of-00002.gguf", "zshai-mini-3b-v3-Q4_K_M-00002-of-00002.gguf"} {
		os.WriteFile(filepath.Join(d, "models", f), []byte("x"), 0o644)
	}
	inst := Installed()
	if len(inst) != 3 || filepath.Base(inst["3b"]) != "zshai-mini-3b-v3-Q4_K_M-00001-of-00002.gguf" {
		t.Fatalf("installed: %v", inst)
	}
	name, _, err := Pick()
	want := "1.5b"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		want = "3b"
	}
	if err != nil || name != want {
		t.Errorf("pick: %s %v, want %s", name, err, want)
	}
	if err := Remove("3b"); err != nil || len(Installed()) != 2 {
		t.Errorf("remove 3b: %v, left %v", err, Installed())
	}
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"localModel": "3b"}`), 0o644)
	if _, _, err := Pick(); err == nil {
		t.Error("3b isn't installed, but Pick found it")
	}
}
