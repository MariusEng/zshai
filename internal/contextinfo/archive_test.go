package contextinfo

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveLine(t *testing.T) {
	d := t.TempDir()
	zp := filepath.Join(d, "photos.zip")
	f, _ := os.Create(zp)
	w := zip.NewWriter(f)
	for _, n := range []string{"trip/", "trip/a.jpg", "trip/b.jpg", "trip/notes/x.pdf"} {
		fw, _ := w.Create(n)
		if !strings.HasSuffix(n, "/") {
			fw.Write([]byte("0123456789"))
		}
	}
	w.Close()
	f.Close()
	got := archiveLine(zp)
	for _, want := range []string{"zip archive: 3 files", "30 B unpacked", "inside one folder trip/", "trip/notes/x.pdf"} {
		if !strings.Contains(got, want) {
			t.Errorf("zip: %q lacks %q", got, want)
		}
	}

	tp := filepath.Join(d, "backup.tar.gz")
	f, _ = os.Create(tp)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"a.txt", "docs/b.md", "c.csv"} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: 3})
		tw.Write([]byte("abc"))
	}
	tw.Close()
	gz.Close()
	f.Close()
	got = archiveLine(tp)
	for _, want := range []string{"tar.gz archive: 3 files", "top level: a.txt docs/ c.csv", "docs/b.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("tar.gz: %q lacks %q", got, want)
		}
	}
	if archiveLine(filepath.Join(d, "notes.txt")) != "" {
		t.Error("not an archive")
	}
}

// list7z reads 7-Zip's technical listing (7zz l -ba -slt); a stand-in 7zz prints one.
func TestArchiveLine7z(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "7zz"), []byte(`#!/bin/sh
cat <<'OUT'
Path = project
Folder = +
Size = 0
Attributes = D

Path = project/src/main.go
Folder = -
Size = 2048
Attributes = A

Path = project/README.md
Folder = -
Size = 1000
Attributes = A
OUT
`), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	p := filepath.Join(t.TempDir(), "project.7z")
	os.WriteFile(p, []byte("7z"), 0o644)
	got := archiveLine(p)
	for _, want := range []string{"7z archive: 2 files", "inside one folder project/", "project/src/main.go", "3.0 KB unpacked"} {
		if !strings.Contains(got, want) {
			t.Errorf("7z: %q lacks %q", got, want)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got := archiveLine(p); !strings.Contains(got, "7-Zip is not installed") {
		t.Errorf("without 7-Zip: %q", got)
	}
}
