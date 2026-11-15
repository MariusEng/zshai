// Package theme holds zshai's colour schemes: SGR codes for each role the plugin
// prints (PLAN.md §1.5). zshai never touches the prompt; these only colour zshai's own lines.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Roles, in the order the preview shows them.
//
//	cmd      the ▸ command line in show/explain mode
//	explain  the explanation under it
//	ask      questions, clarifying questions
//	note     zshai's quiet lines (▸ in confirm, hints, results)
//	danger   dangerous commands and errors
//	yes, no, show, edit   the key letters in "ja / nei / vis / endre"
var Roles = []string{"cmd", "explain", "ask", "note", "danger", "yes", "no", "show", "edit"}

// keys per theme: yes, no, show, edit
func k(yes, no, show, edit string) map[string]string {
	return map[string]string{"yes": yes, "no": no, "show": show, "edit": edit}
}

type Theme struct {
	Name  string
	About [2]string // no, en
	C     map[string]string
}

// 256-colour codes so they look the same in every terminal theme that supports them
// (ghostty, kitty, iTerm2, Terminal.app, VS Code, gnome-terminal).
var list = []Theme{
	{"gold", [2]string{"gull på mørk bakgrunn (standard på mørk)", "gold on a dark background (default on dark)"},
		with(k("1;38;5;114", "1;38;5;203", "1;38;5;220", "1;38;5;117"), map[string]string{"cmd": "1;97", "explain": "38;5;220", "ask": "38;5;214", "note": "38;5;245", "danger": "1;38;5;203"})},
	{"light", [2]string{"for lys bakgrunn (standard på lys)", "for a light background (default on light)"},
		with(k("1;38;5;28", "1;38;5;160", "1;38;5;136", "1;38;5;25"), map[string]string{"cmd": "1;30", "explain": "38;5;25", "ask": "38;5;130", "note": "38;5;242", "danger": "1;38;5;160"})},
	{"classic", [2]string{"terminalens egne farger (grå, gul, rød)", "the terminal's own colours (grey, yellow, red)"},
		with(k("1;32", "1;31", "1;33", "1;36"), map[string]string{"cmd": "1", "explain": "97", "ask": "33", "note": "90", "danger": "1;31"})},
	{"solarized", [2]string{"Solarized-paletten", "the Solarized palette"},
		with(k("1;38;5;64", "1;38;5;160", "1;38;5;136", "1;38;5;33"), map[string]string{"cmd": "1;38;5;33", "explain": "38;5;37", "ask": "38;5;136", "note": "38;5;244", "danger": "1;38;5;160"})},
	{"nord", [2]string{"kjølige Nord-farger", "cool Nord colours"},
		with(k("1;38;5;108", "1;38;5;167", "1;38;5;222", "1;38;5;110"), map[string]string{"cmd": "1;38;5;153", "explain": "38;5;110", "ask": "38;5;222", "note": "38;5;103", "danger": "1;38;5;167"})},
	{"dracula", [2]string{"Dracula-farger", "Dracula colours"},
		with(k("1;38;5;84", "1;38;5;203", "1;38;5;228", "1;38;5;117"), map[string]string{"cmd": "1;38;5;231", "explain": "38;5;141", "ask": "38;5;228", "note": "38;5;61", "danger": "1;38;5;203"})},
	{"mono", [2]string{"ingen farger, bare fet og svak skrift", "no colours, only bold and dim"},
		with(k("1;4", "1;4", "1;4", "1;4"), map[string]string{"cmd": "1", "explain": "0", "ask": "1", "note": "2", "danger": "1;4"})},
}

func with(a, b map[string]string) map[string]string {
	for k, v := range a {
		b[k] = v
	}
	return b
}

// Keys colours the first letter (the key to press) of each choice in "ja / nei / vis / endre".
// A line without " / " (the typed JA/YES question) is left as it is.
func Keys(t Theme, keys string) string {
	if !strings.Contains(keys, " / ") {
		return keys
	}
	role := map[byte]string{'j': "yes", 'y': "yes", 'n': "no", 'v': "show", 's': "show", 'e': "edit"}
	parts := strings.Split(keys, " / ")
	for i, p := range parts {
		if p == "" {
			continue
		}
		if r, ok := role[p[0]]; ok {
			parts[i] = sgr(t.C[r], p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " / ")
}

// Auto is the default: gold on a dark background, light on a light one.
const Auto = "auto"

func All() []Theme { return list }

// Names returns "auto" plus every theme, in menu order.
func Names() []string {
	n := []string{Auto}
	for _, t := range list {
		n = append(n, t.Name)
	}
	return n
}

func Get(name string) (Theme, bool) {
	for _, t := range list {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// Resolve picks the theme to use: an unknown name falls back to auto, and auto
// follows the background ("dark", "light", or "" when unknown, which counts as dark).
func Resolve(name, bg string) Theme {
	if t, ok := Get(name); ok {
		return t
	}
	if bg == "light" {
		t, _ := Get("light")
		return t
	}
	t, _ := Get("gold")
	return t
}

// Zsh renders the theme as an assignment for the plugin to eval.
func Zsh(t Theme, auto bool) string {
	keys := make([]string, 0, len(t.C))
	for k := range t.C {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("ZSHAI_COLORS=(")
	for _, k := range keys {
		fmt.Fprintf(&b, " %s '%s'", k, t.C[k])
	}
	a := "0"
	if auto {
		a = "1"
	}
	fmt.Fprintf(&b, " _name '%s' _auto '%s' )\n", t.Name, a)
	return b.String()
}

func sgr(code, s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }

// Preview draws one theme the way zshai's lines look with it.
func Preview(t Theme, no bool) string {
	ex, q, keys, note, danger := "viser bare mappene (*/ matcher bare mapper)", "Skal jeg slette 2 filer (117 KB)?",
		"ja / nei / vis / endre", "(ikke kjørt; ↑ henter kommandoen)", "farlig: sletter hele hjemmemappa"
	if !no {
		ex, q, keys, note, danger = "lists only the folders (*/ only matches folders)", "Delete 2 files (117 KB)?",
			"yes / no / show / edit", "(not run; ↑ brings it back)", "dangerous: deletes your whole home folder"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "      %s\n", sgr(t.C["cmd"], "▸ ls -d */"))
	fmt.Fprintf(&b, "        %s\n", sgr(t.C["explain"], ex))
	fmt.Fprintf(&b, "      %s  %s\n", sgr(t.C["ask"], q), Keys(t, keys))
	fmt.Fprintf(&b, "      %s   %s\n", sgr(t.C["note"], note), sgr(t.C["danger"], danger))
	return b.String()
}
