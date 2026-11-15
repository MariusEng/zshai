// Package config reads and writes ~/.config/zshai/config.json (PLAN.md §1.3).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Language               string   `json:"language"` // "en" | "no"
	Server                 string   `json:"server"`   // OpenAI-compatible base URL
	Model                  string   `json:"model"`    // model id on that server
	APIKey                 string   `json:"apiKey,omitempty"`
	TimeoutSeconds         int      `json:"timeoutSeconds"` // per model call
	Tools                  string   `json:"tools"`          // "installed" | "shipped"
	SuggestInstall         string   `json:"suggestInstall"` // "ask" | "mention" | "never"
	PackageManager         string   `json:"packageManager"` // "auto" | "apt" | "pacman" | "brew" | "dnf"
	AUR                    bool     `json:"aur"`
	FixAttempts            int      `json:"fixAttempts"`
	HistoryNote            bool     `json:"historyNote"`
	ProtectedSubscriptions []string `json:"protectedSubscriptions"`
	Notes                  []string `json:"notes"`
	Log                    bool     `json:"log"`
	// AutoDetect: also send plain-language lines to zshai without the "zshai:" prefix.
	AutoDetect bool `json:"autoDetect"`
	// Theme: zshai's colours (internal/theme); "auto" = gold on dark, light on light.
	Theme string `json:"theme"`
}

func Dir() string {
	if d := os.Getenv("ZSHAI_CONFIG_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "zshai")
}

func StateDir() string {
	if d := os.Getenv("ZSHAI_STATE_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "zshai")
}

func CacheDir() string {
	if d := os.Getenv("ZSHAI_CACHE_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "zshai")
}

func Path() string { return filepath.Join(Dir(), "config.json") }

func Defaults() Config {
	return Config{
		Language:       defaultLanguage(),
		Server:         "http://localhost:1234/v1", // LM Studio's default; any OpenAI-compatible server works
		Model:          "qwen/qwen3-coder-30b",
		TimeoutSeconds: 30,
		Tools:          "installed",
		SuggestInstall: "ask",
		PackageManager: "auto",
		FixAttempts:    1,
		Log:            true,
		Theme:          "auto",
	}
}

// defaultLanguage: psai's choice if psai is configured, else from $LANG.
func defaultLanguage() string {
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".config", "psai", "config.json")); err == nil {
		var p struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(b, &p) == nil && (p.Language == "no" || p.Language == "en") {
			return p.Language
		}
	}
	l := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LANG"))
	if strings.HasPrefix(l, "nb") || strings.HasPrefix(l, "nn") || strings.HasPrefix(l, "no") {
		return "no"
	}
	return "en"
}

// Load reads the config, filling in defaults for missing keys.
func Load() Config {
	c := Defaults()
	if b, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if v := os.Getenv("ZSHAI_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("ZSHAI_MODEL"); v != "" {
		c.Model = v
	}
	if c.Language != "no" {
		c.Language = "en"
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 30
	}
	return c
}

func Save(c Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}
