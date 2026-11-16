// Package prompt builds the system prompt from the user's template,
// ~/.config/zshai/prompt.txt (edited with `zshai: prompt`).
//
// The template is plain text with placeholders. {zshai} expands to zshai's built-in rules
// and answer format (the long one, or the short one for zshai-mini); without {zshai} the
// file replaces the whole prompt. Lines starting with # are comments. The values are fixed
// per machine, so the rendered prompt stays byte-identical between requests and the model
// server can reuse its cache.
package prompt

import (
	"cmp"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/MariusEng/zshai/internal/config"
)

// Default is written to prompt.txt the first time it is edited, and used while there is none.
const Default = `# zshai's system prompt. Edit freely and save; it is used from the next request.
# Lines starting with # are comments. Placeholders:
#   {zshai}     zshai's built-in rules and answer format (keep it unless you replace everything)
#   {os} {arch} {flavour} {shell} {packages} {language} {user} {host} {home}
# zshai: prompt show   prints the prompt as the model gets it; zshai: prompt reset   restores this file.
{zshai}

About this machine and user:
- OS: {os} on {arch} ({flavour}); shell: {shell}; packages: {packages}.
- Write questions and explanations in {language}.
- User: {user} on {host}; home folder {home}.
`

var placeholder = regexp.MustCompile(`\{[a-z]+\}`)

func Path() string { return filepath.Join(config.Dir(), "prompt.txt") }

// Template returns the user's template, or Default when there is none.
func Template() (string, bool) {
	b, err := os.ReadFile(Path())
	if err != nil {
		return Default, false
	}
	return string(b), true
}

// Values are the placeholders' values.
type Values map[string]string

// MachineValues fills the placeholders that don't come from the request context.
func MachineValues(osName, flavour, packages, language string) Values {
	v := Values{"os": osName, "flavour": flavour, "packages": packages, "arch": arch()}
	if v["packages"] == "" {
		v["packages"] = "none"
	}
	v["language"] = "English"
	if language == "no" {
		v["language"] = "Norwegian Bokmål"
	}
	v["shell"] = "zsh"
	if z := cmp.Or(os.Getenv("ZSHAI_ZSH_VERSION"), os.Getenv("ZSH_VERSION")); z != "" {
		v["shell"] = "zsh " + z
	}
	if u, err := user.Current(); err == nil {
		v["user"] = u.Username
	}
	v["host"], _ = os.Hostname()
	v["host"] = strings.TrimSuffix(strings.SplitN(v["host"], ".", 2)[0], ".local")
	v["home"] = "~"
	return v
}

func arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	}
	return runtime.GOARCH
}

// Render builds the system prompt: comments dropped, {zshai} = core, placeholders filled.
func Render(tmpl, core string, v Values) string {
	var lines []string
	for _, l := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		lines = append(lines, l)
	}
	s := strings.TrimSpace(strings.Join(lines, "\n"))
	s = placeholder.ReplaceAllStringFunc(s, func(p string) string {
		k := p[1 : len(p)-1]
		if k == "zshai" {
			return strings.TrimSpace(core)
		}
		if val, ok := v[k]; ok {
			return val
		}
		return p
	})
	return s
}

// Problems lists what will go wrong with a template: unknown placeholders, or a full
// replacement without zshai's answer format.
func Problems(tmpl string) []string {
	var out []string
	known := map[string]bool{"zshai": true, "os": true, "arch": true, "flavour": true, "shell": true, "packages": true,
		"language": true, "user": true, "host": true, "home": true}
	seen := map[string]bool{}
	for _, l := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		for _, p := range placeholder.FindAllString(l, -1) {
			if k := p[1 : len(p)-1]; !known[k] && !seen[k] {
				seen[k] = true
				out = append(out, "unknown placeholder "+p)
			}
		}
	}
	if !strings.Contains(Render(tmpl, "{zshai}", nil), "{zshai}") && !strings.Contains(tmpl, "---") {
		out = append(out, "no {zshai} and no answer format: zshai can't read the replies (zshai: prompt reset)")
	}
	sort.Strings(out)
	return out
}

// Ensure writes the default template if there is no prompt.txt yet, and returns its path.
func Ensure() (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil {
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, []byte(Default), 0o644)
}

// Reset restores the default template.
func Reset() (string, error) {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, []byte(Default), 0o644)
}
