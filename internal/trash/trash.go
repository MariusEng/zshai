// Package trash moves files to the trash instead of deleting them (PLAN.md §2.3).
// Linux: the freedesktop.org Trash spec (~/.local/share/Trash). macOS: Finder via
// osascript (so "Put Back" works), falling back to a plain move into ~/.Trash.
package trash

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Moved records where a file went, for undo.
type Moved struct {
	From string `json:"from"`
	To   string `json:"to"`
	Info string `json:"info,omitempty"` // .trashinfo file (Linux)
}

// Put trashes the given absolute paths.
// ZSHAI_TRASH_DIR (tests) puts everything in a freedesktop-style trash there, on any OS.
func Put(paths []string) ([]Moved, error) {
	if os.Getenv("ZSHAI_TRASH_DIR") != "" {
		return putXDG(paths)
	}
	if runtime.GOOS == "darwin" {
		return putMac(paths)
	}
	return putXDG(paths)
}

func xdgDir() string {
	if d := os.Getenv("ZSHAI_TRASH_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "Trash")
}

func putXDG(paths []string) ([]Moved, error) {
	dir := xdgDir()
	files, infos := filepath.Join(dir, "files"), filepath.Join(dir, "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(infos, 0o700); err != nil {
		return nil, err
	}
	var done []Moved
	for _, p := range paths {
		name := filepath.Base(p)
		dest, info := "", ""
		for i := 1; ; i++ {
			cand := name
			if i > 1 {
				ext := filepath.Ext(name)
				cand = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(name, ext), i, ext)
			}
			info = filepath.Join(infos, cand+".trashinfo")
			f, err := os.OpenFile(info, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					continue
				}
				return done, err
			}
			u := &url.URL{Path: p}
			fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", u.EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
			f.Close()
			dest = filepath.Join(files, cand)
			break
		}
		if err := os.Rename(p, dest); err != nil {
			os.Remove(info)
			if errors.Is(err, syscall.EXDEV) {
				if gio, e := exec.LookPath("gio"); e == nil {
					if e := exec.Command(gio, "trash", p).Run(); e == nil {
						done = append(done, Moved{From: p, To: "gio-trash"})
						continue
					}
				}
				return done, fmt.Errorf("%s is on another disk and can't be moved to the trash", p)
			}
			return done, err
		}
		done = append(done, Moved{From: p, To: dest, Info: info})
	}
	return done, nil
}

func putMac(paths []string) ([]Moved, error) {
	home, _ := os.UserHomeDir()
	trashDir := filepath.Join(home, ".Trash")
	var done []Moved
	// Finder keeps "Put Back" information; do it in one osascript call when the list is small.
	if len(paths) <= 200 {
		var items []string
		for _, p := range paths {
			items = append(items, fmt.Sprintf("POSIX file %q", p))
		}
		script := fmt.Sprintf(`tell application "Finder" to set r to delete {%s}
set out to ""
repeat with i in (r as list)
set out to out & POSIX path of (i as alias) & linefeed
end repeat
return out`, strings.Join(items, ", "))
		if out, err := exec.Command("osascript", "-e", script).Output(); err == nil {
			got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			for i, p := range paths {
				to := ""
				if i < len(got) {
					to = strings.TrimSuffix(got[i], "/")
				}
				done = append(done, Moved{From: p, To: to})
			}
			return done, nil
		}
	}
	for _, p := range paths {
		name := filepath.Base(p)
		dest := filepath.Join(trashDir, name)
		for i := 2; ; i++ {
			if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
				break
			}
			ext := filepath.Ext(name)
			dest = filepath.Join(trashDir, fmt.Sprintf("%s %d%s", strings.TrimSuffix(name, ext), i, ext))
		}
		if err := os.Rename(p, dest); err != nil {
			return done, err
		}
		done = append(done, Moved{From: p, To: dest})
	}
	return done, nil
}

// Restore moves trashed files back. It refuses to overwrite.
func Restore(ms []Moved) (int, []string) {
	n := 0
	var problems []string
	for _, m := range ms {
		if m.To == "" || m.To == "gio-trash" {
			problems = append(problems, m.From+": restore it from the trash yourself")
			continue
		}
		if _, err := os.Lstat(m.From); err == nil {
			problems = append(problems, m.From+": already exists, not overwritten")
			continue
		}
		_ = os.MkdirAll(filepath.Dir(m.From), 0o755)
		if err := os.Rename(m.To, m.From); err != nil {
			problems = append(problems, m.From+": "+err.Error())
			continue
		}
		if m.Info != "" {
			os.Remove(m.Info)
		}
		n++
	}
	return n, problems
}
