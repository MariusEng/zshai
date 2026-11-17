// Package sandbox test-runs a command that changes files, before the user is asked
// about it: in a throwaway copy of the current folder, with no network, no keyboard
// and no way to write anywhere else. What it did (files created, changed, deleted) and
// how it went (exit status, errors) is compared with the request before the real run.
//
//   - macOS: sandbox-exec. The folder is cloned (APFS copy-on-write, instant at any
//     size); writes outside the clone, network and system services (defaults, open,
//     Apple Events) are denied.
//   - Linux: bubblewrap (bwrap), when installed. The copy is mounted over the folder's
//     own path, the rest of the file system is read-only, no network. Without bwrap
//     (Ubuntu blocks other unprivileged sandboxes) nothing is test-run.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Limits for the folder that gets copied. A clone on macOS costs nothing, but every
// entry is compared before and after.
const (
	MaxEntries = 20000
	MaxBytes   = 300 << 20 // Linux without reflinks really copies
)

type Effect struct {
	Kind  string // created | modified | deleted | moved
	From  string // moved: where it was
	Path  string // relative to the folder
	Size  int64
	IsDir bool
}

type Result struct {
	Exit     int
	Stdout   string // the first 64 KB
	Stderr   string
	TimedOut bool
	// Denied: it tried to reach outside the copy (network, other folders, system
	// services) and the sandbox said no: the test says nothing about that part.
	Denied  bool
	Effects []Effect
	Copy    string // the copy, for looking at created files; removed by Close
	root    string
}

// Close removes the copy.
func (r *Result) Close() {
	if r != nil && r.root != "" {
		os.RemoveAll(r.root)
	}
}

// Kind is the sandbox this machine has: "sandbox-exec", "bwrap" or "".
func Kind() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err == nil {
			return "sandbox-exec"
		}
	case "linux":
		if p, err := exec.LookPath("bwrap"); err == nil && bwrapWorks(p) {
			return "bwrap"
		}
	}
	return ""
}

func bwrapWorks(p string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, p, "--ro-bind", "/", "/", "--unshare-net", "true").Run() == nil
}

// ErrTooBig: the folder is too big to copy for a test.
var ErrTooBig = errors.New("the folder is too big to test in")

type entry struct {
	size  int64
	mtime int64
	mode  fs.FileMode
	link  string
}

func snapshot(dir string) (map[string]entry, int64, error) {
	m := map[string]entry{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: skip
		}
		if p == dir {
			return nil
		}
		if len(m) >= MaxEntries {
			return ErrTooBig
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		e := entry{size: info.Size(), mtime: info.ModTime().UnixNano(), mode: info.Mode()}
		if info.Mode()&fs.ModeSymlink != 0 {
			e.link, _ = os.Readlink(p)
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		m[rel] = e
		return nil
	})
	return m, total, err
}

// Run test-runs src in a copy of cwd (with pipefail: in  jq … | jq … > out.json  the first
// jq's failure must count, or the test only sees an empty out.json). It returns ErrTooBig, or an error when there is
// no sandbox or the copy fails; the caller then simply doesn't test.
func Run(src, cwd string, timeout time.Duration) (*Result, error) {
	kind := Kind()
	if kind == "" {
		return nil, errors.New("no sandbox on this machine")
	}
	before, total, err := snapshot(cwd)
	if err != nil {
		return nil, err
	}
	if kind == "bwrap" && total > MaxBytes {
		return nil, ErrTooBig
	}
	root, err := os.MkdirTemp("", "zshai-test-")
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r // macOS: /var → /private/var, the sandbox profile needs the real path
	}
	res := &Result{root: root, Copy: filepath.Join(root, "w")}
	tmp := filepath.Join(root, "tmp")
	_ = os.Mkdir(tmp, 0o700)
	var cp *exec.Cmd
	if kind == "sandbox-exec" {
		cp = exec.Command("cp", "-c", "-R", "-p", cwd, res.Copy)
	} else {
		cp = exec.Command("cp", "-a", "--reflink=auto", cwd, res.Copy)
	}
	if out, err := cp.CombinedOutput(); err != nil {
		res.Close()
		return nil, errors.New("copy failed: " + strings.TrimSpace(string(out)))
	}
	// the copy's own timestamps are what the command is compared against
	before, _, _ = snapshot(res.Copy)

	zsh := "/bin/zsh"
	if p, err := exec.LookPath("zsh"); err == nil {
		zsh = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	env := append(os.Environ(), "HISTFILE=/dev/null")
	if kind == "sandbox-exec" {
		cmd = exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", darwinProfile(root), zsh, "-f", "-o", "pipefail", "-c", src)
		cmd.Dir = res.Copy
		env = append(env, "TMPDIR="+tmp+"/", "TMPPREFIX="+tmp+"/zsh")
	} else {
		bw, _ := exec.LookPath("bwrap")
		cmd = exec.CommandContext(ctx, bw, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
			"--bind", tmp, "/tmp", "--bind", res.Copy, cwd, "--unshare-net", "--unshare-pid", "--die-with-parent",
			"--new-session", "--chdir", cwd, zsh, "-f", "-o", "pipefail", "-c", src)
		env = append(env, "TMPDIR=/tmp", "TMPPREFIX=/tmp/zsh")
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout, stderr limited
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	res.TimedOut = ctx.Err() != nil
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		res.Exit = ee.ExitCode()
	} else if err != nil && !res.TimedOut {
		res.Close()
		return nil, err
	}
	res.Stdout, res.Stderr = stdout.String(), stderr.String()
	res.Denied = deniedRe(res.Stderr)

	after, _, _ := snapshot(res.Copy)
	for p, a := range after {
		b, ok := before[p]
		switch {
		case !ok:
			res.Effects = append(res.Effects, Effect{Kind: "created", Path: p, Size: a.size, IsDir: a.mode.IsDir()})
		case !a.mode.IsDir() && (a.size != b.size || a.mtime != b.mtime || a.link != b.link || a.mode != b.mode):
			res.Effects = append(res.Effects, Effect{Kind: "modified", Path: p, Size: a.size})
		}
	}
	for p, b := range before {
		if _, ok := after[p]; ok {
			continue
		}
		// a deleted folder is one effect, not one per file in it
		if par := filepath.Dir(p); par != "." {
			if _, ok := after[par]; !ok {
				continue
			}
		}
		res.Effects = append(res.Effects, Effect{Kind: "deleted", Path: p, Size: b.size, IsDir: b.mode.IsDir()})
	}
	res.Effects = collapse(pairMoves(res.Effects, before, after))
	sort.Slice(res.Effects, func(i, j int) bool { return res.Effects[i].Path < res.Effects[j].Path })
	return res, nil
}

// pairMoves turns a deleted entry and a created one with the same name, kind and size
// into one move (mv logs arkiv/ → "moved logs → arkiv/logs").
func pairMoves(effs []Effect, before, after map[string]entry) []Effect {
	used := map[int]bool{}
	var out []Effect
	for i, d := range effs {
		if d.Kind != "deleted" {
			continue
		}
		for j, c := range effs {
			if c.Kind == "created" && !used[j] && c.IsDir == d.IsDir && (c.IsDir || c.Size == d.Size) &&
				filepath.Base(c.Path) == filepath.Base(d.Path) {
				used[i], used[j] = true, true
				out = append(out, Effect{Kind: "moved", Path: c.Path, From: d.Path, Size: c.Size, IsDir: c.IsDir})
				break
			}
		}
	}
	for i, e := range effs {
		if !used[i] {
			out = append(out, e)
		}
	}
	return out
}

// collapse: a new folder is one effect, not one per file in it (also inside a moved one).
func collapse(effs []Effect) []Effect {
	tops := map[string]bool{}
	for _, e := range effs {
		if (e.Kind == "created" || e.Kind == "moved") && e.IsDir {
			tops[e.Path] = true
		}
	}
	var out []Effect
	for _, e := range effs {
		inside := false
		for d := filepath.Dir(e.Path); d != "." && d != "/"; d = filepath.Dir(d) {
			if tops[d] && e.Kind == "created" {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, e)
		}
	}
	return out
}

// darwinProfile: everything as usual, except: no network, no writes outside root (and
// /dev/null), no system services but name lookups and logging.
func darwinProfile(root string) string {
	q := strings.ReplaceAll(root, `"`, `\"`)
	return `(version 1)(allow default)(deny network*)(deny file-write*)` +
		`(allow file-write* (subpath "` + q + `") (literal "/dev/null") (literal "/dev/zero") (literal "/dev/dtracehelper"))` +
		`(deny mach-lookup (with no-report))` +
		`(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo") (global-name "com.apple.system.logger")` +
		` (global-name "com.apple.system.notification_center"))`
}

func deniedRe(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"operation not permitted", "read-only file system", "could not resolve host",
		"network is unreachable", "temporary failure in name resolution", "could not write domain",
		"nodename nor servname", "sandbox"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

type limited struct{ bytes.Buffer }

func (l *limited) Write(p []byte) (int, error) {
	if room := 64<<10 - l.Len(); room > 0 {
		l.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Untestable: commands whose effects aren't files in the folder (system settings,
// processes, other machines), or that wait for a person. They are never test-run.
var Untestable = map[string]bool{
	"sudo": true, "doas": true, "su": true, "ssh": true, "scp": true, "sftp": true, "mosh": true,
	"open": true, "xdg-open": true, "osascript": true, "defaults": true, "launchctl": true, "systemctl": true,
	"service": true, "kill": true, "killall": true, "pkill": true, "reboot": true, "shutdown": true, "halt": true,
	"crontab": true, "at": true, "pmset": true, "networksetup": true, "scutil": true, "diskutil": true, "tmutil": true,
	"mdutil": true, "spctl": true, "csrutil": true, "nvram": true, "dscl": true, "security": true, "pbcopy": true,
	"say": true, "shortcuts": true, "brew": true, "apt": true, "apt-get": true, "pacman": true, "yay": true,
	"dnf": true, "port": true, "snap": true, "flatpak": true, "docker": true, "podman": true, "kubectl": true,
	"az": true, "gh": true, "aws": true, "gcloud": true, "vim": true, "vi": true, "nvim": true, "nano": true,
	"emacs": true, "less": true, "more": true, "man": true, "top": true, "htop": true, "btop": true, "watch": true,
	"tmux": true, "screen": true, "fzf": true, "code": true, "gsettings": true, "dbus-send": true,
	"notify-send": true,
}
