package contextinfo

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// archiveLine tells the model what is inside a named archive (.zip, .tar[.gz|.bz2], .tgz,
// .7z, .rar): how many files, how big unpacked, the top-level entries and the first file
// names. "Extract only the PDFs from backup.7z" then names real paths, and the model can
// see whether the archive has its own top folder (so extracting into -oNAME nests it).
func archiveLine(p string) string {
	lower := strings.ToLower(p)
	var a archive
	var err error
	switch {
	case strings.HasSuffix(lower, ".zip"):
		a, err = listZip(p)
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		a, err = listTar(p, lower)
	case strings.HasSuffix(lower, ".7z"), strings.HasSuffix(lower, ".rar"):
		a, err = list7z(p)
	default:
		return ""
	}
	if err != nil {
		if a.kind != "" && a.why != "" {
			return fmt.Sprintf(" — %s archive (%s)", a.kind, a.why)
		}
		return ""
	}
	return a.String()
}

type archive struct {
	kind, why   string
	files, dirs int
	size        int64
	top         []string // top-level entries, folders with "/"
	first       []string // the first file paths
	topSeen     map[string]bool
	more        bool
}

const maxEntries = 20000

func (a *archive) add(name string, dir bool, size int64) {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	if name == "" || name == "." {
		return
	}
	if a.topSeen == nil {
		a.topSeen = map[string]bool{}
	}
	top, _, nested := strings.Cut(name, "/")
	if nested || dir {
		top += "/"
	}
	if !a.topSeen[top] && len(a.top) < 8 {
		a.top = append(a.top, top)
	}
	a.topSeen[top] = true
	if dir {
		a.dirs++
		return
	}
	a.files++
	a.size += size
	if len(a.first) < 8 {
		a.first = append(a.first, name)
	}
}

func (a archive) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, " — %s archive: %d files", a.kind, a.files)
	if a.more {
		b.WriteString(" or more")
	}
	if a.size > 0 {
		fmt.Fprintf(&b, ", %s unpacked", HumanSize(a.size))
	}
	if len(a.topSeen) == 1 && strings.HasSuffix(a.top[0], "/") {
		fmt.Fprintf(&b, "; everything is inside one folder %s", a.top[0])
	} else if len(a.top) > 0 {
		b.WriteString("; top level: " + strings.Join(a.top, " "))
		if len(a.topSeen) > len(a.top) {
			fmt.Fprintf(&b, " …+%d", len(a.topSeen)-len(a.top))
		}
	}
	if len(a.first) > 0 {
		b.WriteString("; files: " + strings.Join(a.first, ", "))
		if a.files > len(a.first) {
			b.WriteString(", …")
		}
	}
	return b.String()
}

func listZip(p string) (archive, error) {
	a := archive{kind: "zip"}
	r, err := zip.OpenReader(p)
	if err != nil {
		return a, err
	}
	defer r.Close()
	for i, f := range r.File {
		if i >= maxEntries {
			a.more = true
			break
		}
		if f.Flags&1 != 0 && a.why == "" {
			a.why = "encrypted"
		}
		a.add(f.Name, f.FileInfo().IsDir(), int64(f.UncompressedSize64))
	}
	if a.why == "encrypted" {
		a.kind = "zip (password-protected)"
	}
	return a, nil
}

func listTar(p, lower string) (archive, error) {
	a := archive{kind: "tar"}
	f, err := os.Open(p)
	if err != nil {
		return a, err
	}
	defer f.Close()
	var r io.Reader = bufio.NewReader(f)
	switch {
	case strings.HasSuffix(lower, "gz"):
		a.kind = "tar.gz"
		gz, err := gzip.NewReader(r)
		if err != nil {
			return a, err
		}
		r = gz
	case strings.HasSuffix(lower, "bz2"):
		a.kind = "tar.bz2"
		r = bzip2.NewReader(r)
	}
	tr := tar.NewReader(r)
	deadline := time.Now().Add(2 * time.Second) // a big compressed tar is read to the end
	for n := 0; ; n++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if n == 0 {
				return a, err
			}
			break
		}
		if n >= maxEntries || time.Now().After(deadline) {
			a.more = true
			break
		}
		a.add(h.Name, h.Typeflag == tar.TypeDir, h.Size)
	}
	return a, nil
}

// list7z asks the installed 7-Zip (7zz / 7z / 7za); without one there is nothing to read.
func list7z(p string) (archive, error) {
	kind := strings.TrimPrefix(strings.ToLower(path.Ext(p)), ".")
	a := archive{kind: kind}
	bin := ""
	for _, n := range []string{"7zz", "7z", "7za"} {
		if _, err := exec.LookPath(n); err == nil {
			bin = n
			break
		}
	}
	if bin == "" {
		a.why = "7-Zip is not installed, so its contents are unknown"
		return a, fmt.Errorf("no 7-Zip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// -p: a wrong password instead of a prompt for an encrypted archive; stdin is empty
	out, err := exec.CommandContext(ctx, bin, "l", "-ba", "-slt", "-pzshai", "--", p).Output()
	if err != nil && len(out) == 0 {
		if strings.Contains(strings.ToLower(string(out)), "password") || ctx.Err() == nil {
			a.why = "could not be listed; it may be password-protected"
		}
		return a, err
	}
	var name string
	var size int64
	var dir bool
	flush := func() {
		if name != "" {
			a.add(name, dir, size)
		}
		name, size, dir = "", 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		k, v, ok := strings.Cut(line, " = ")
		switch {
		case !ok && strings.TrimSpace(line) == "":
			flush()
		case k == "Path":
			flush()
			name = v
		case k == "Size":
			fmt.Sscan(v, &size)
		case k == "Folder":
			dir = v == "+"
		case k == "Attributes":
			dir = dir || strings.HasPrefix(v, "D")
		}
		if a.files+a.dirs >= maxEntries {
			a.more = true
			break
		}
	}
	flush()
	return a, nil
}
