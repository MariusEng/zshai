// Package localmodel runs zshai's bundled model: llama.cpp's llama-server with the
// zshai-mini GGUF, on 127.0.0.1:8093. zshai starts it on the first request and leaves it
// running (a cold start costs a second or two; a warm request ~0.2–0.5 s).
//
// Files, searched in this order (the first that has them wins):
//
//	$ZSHAI_DATA_DIR
//	<dir of the zshai binary>/../share/zshai   (make install / release tarball)
//	$XDG_DATA_HOME/zshai, default ~/.local/share/zshai
//
// holding llama-server (or llama-server on PATH) and models/zshai-mini*.gguf.
package localmodel

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MariusEng/zshai/internal/config"
)

const (
	Port  = 8093
	Model = "zshai-mini"
)

// IsLocal: the config asks for the bundled model ("local", or no server at all).
func IsLocal(server string) bool {
	s := strings.ToLower(strings.TrimSpace(server))
	return s == "" || s == "local" || s == "builtin"
}

func URL() string { return fmt.Sprintf("http://127.0.0.1:%d/v1", Port) }

// DataDirs lists where the server and model are looked for.
func DataDirs() []string {
	var ds []string
	if d := os.Getenv("ZSHAI_DATA_DIR"); d != "" {
		ds = append(ds, d)
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			ds = append(ds, filepath.Join(filepath.Dir(exe), "..", "share", "zshai"))
		}
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return append(ds, filepath.Join(base, "zshai"))
}

// Files finds llama-server and the model to run (Pick).
func Files() (server, model string, err error) {
	for _, d := range DataDirs() {
		if p := filepath.Join(d, "llama-server"); isExec(p) {
			server = p
			break
		}
	}
	if server == "" {
		if p, e := exec.LookPath("llama-server"); e == nil {
			server = p
		}
	}
	_, model, err = Pick()
	if err == nil && server == "" {
		err = errors.New("llama-server is not installed (run the release's install.sh)")
	}
	return
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func pidFile() string { return filepath.Join(config.StateDir(), "llama-server.pid") }
func LogFile() string { return filepath.Join(config.StateDir(), "llama-server.log") }

// Healthy: the server answers /health with 200 (it answers 503 while loading).
func Healthy() bool {
	c := http.Client{Timeout: 700 * time.Millisecond}
	r, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/health", Port))
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == 200
}

// PID of the server zshai started, if it is still running.
func PID() int {
	b, err := os.ReadFile(pidFile())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}
	return pid
}

func modelFile() string { return filepath.Join(config.StateDir(), "llama-server.model") }

// Ensure starts the server if it isn't running and waits until it answers. A server that
// runs another model than the one chosen now (zshai model use 3b) is restarted.
func Ensure(wait time.Duration) error {
	if _, model, err := Files(); err == nil && PID() > 0 {
		if b, _ := os.ReadFile(modelFile()); string(b) != model {
			Stop()
			time.Sleep(300 * time.Millisecond)
		}
	}
	if Healthy() {
		return nil
	}
	if PID() == 0 {
		if err := Start(); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if Healthy() {
			return nil
		}
		if PID() == 0 {
			return fmt.Errorf("llama-server stopped while starting; see %s", LogFile())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("llama-server did not get ready in %s; see %s", wait, LogFile())
}

// Start launches llama-server in its own session, so it outlives this zshai process.
func Start() error {
	server, model, err := Files()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(LogFile(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(Port),
		"-c", "4096", "-np", "1", "--no-webui", "--alias", Model}
	if runtime.GOOS == "darwin" {
		args = append(args, "-ngl", "99") // Metal
	}
	cmd := exec.Command(server, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	_ = os.WriteFile(modelFile(), []byte(model), 0o644)
	return cmd.Process.Release()
}

// Stop ends the server zshai started.
func Stop() bool {
	pid := PID()
	if pid == 0 {
		return false
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	os.Remove(pidFile())
	return true
}

// Status is one line for `zshai model` and `zshai: status`.
func Status() string {
	server, model, err := Files()
	if err != nil {
		return "local model: " + err.Error()
	}
	state := "not running (starts on the first request)"
	if Healthy() {
		state = fmt.Sprintf("running on 127.0.0.1:%d", Port)
		if pid := PID(); pid > 0 {
			state += fmt.Sprintf(" (pid %d)", pid)
		}
	}
	size := ""
	if st, e := os.Stat(model); e == nil {
		size = fmt.Sprintf(", %.0f MB", float64(st.Size())/1e6)
	}
	return fmt.Sprintf("local model: %s%s; %s; server %s", filepath.Base(model), size, state, server)
}
