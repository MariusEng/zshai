// Package router decides whether a line is a shell command or a request for
// zshai (PLAN.md §2.1). The plugin handles the cheap cases itself (prefixes,
// first word not a command); this decides the hard case: the first word IS a
// command ("find all pdfs bigger than 10 MB").
//
// Release rule: a real shell command must never be sent to the AI.
package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

type Env struct {
	Cwd string
	// IsCommand reports whether a word is a command/alias/function/builtin.
	// nil = look it up on PATH (used by tests and `zshai route`).
	IsCommand func(string) bool
}

type Decision struct {
	AI   bool
	Hint bool // show "interpreted as a question (, to run as a command)"
}

// Words that show up in requests but almost never as arguments to commands.
var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		the a an all my our your this that these those every any some which what whats where when who why how
		is are was were be been do does did can could should would will please me i we you it its them they
		than then bigger smaller larger older newer biggest largest smallest oldest newest latest recent recently
		from into onto with without using about over under between before after since today yesterday
		files folders folder directory directories file lines rows words photos pictures images videos documents
		and or but not no so if many much more most less least only also just still
		of for to in on at by as
		alle alt min mine mitt mitt din dine ditt denne dette disse som hva hvor hvordan hvem hvorfor når hvilke hvilken
		er var blir ble har hadde kan skal vil må kunne skulle ville
		enn større mindre eldre nyere største minste eldste nyeste siste
		fra til med uten om over under mellom før etter siden i dag i går
		filer filene mapper mappen mappa fil linjer rader ord bilder bildene videoer dokumenter
		og eller men ikke ingen så hvis mange mye flere mest færre bare også
		av for på ved som en ett et den det de
	`) {
		stop[w] = true
	}
}

// Decide routes one line.
func Decide(line string, env Env) Decision {
	s := strings.TrimSpace(line)
	if s == "" {
		return Decision{}
	}
	if strings.HasPrefix(s, "?") {
		return Decision{AI: true}
	}
	if strings.HasPrefix(s, ",") || strings.HasPrefix(s, "!") {
		return Decision{}
	}
	isCmd := env.IsCommand
	if isCmd == nil {
		isCmd = func(w string) bool {
			if _, err := exec.LookPath(w); err == nil {
				return true
			}
			return zshBuiltin[w]
		}
	}
	words := strings.Fields(s)
	first := words[0]
	// Unbalanced quotes are prose ("what's in here") — zsh would wait at quote>.
	if strings.Count(s, "'")%2 == 1 && !strings.Contains(s, `\'`) || strings.Count(s, `"`)%2 == 1 {
		// …unless it's clearly a command with flags
		if !hasFlag(words) {
			return Decision{AI: true}
		}
	}
	firstIsCmd := isCmd(first) || strings.ContainsAny(first, "/=$~") || isDir(first, env.Cwd)
	if !firstIsCmd {
		// Number-only words like 69 or 10M35 are answers typed into other programs:
		// never prose. Let the shell say "command not found".
		if looksLikeCode(first) && len(words) == 1 {
			return Decision{}
		}
		return Decision{AI: true}
	}
	// The first word is a command. Only prose-looking lines go to the AI.
	// Shell syntax or balanced quotes: the user is writing a command.
	if strings.ContainsAny(s, "|&;<>()$`\\*{}[]='\"") || hasFlag(words) {
		return Decision{}
	}
	if len(words) < 3 {
		return Decision{}
	}
	nStop, nPath := 0, 0
	for _, w := range words[1:] {
		lw := strings.ToLower(strings.TrimRightFunc(w, unicode.IsPunct))
		if stop[lw] {
			nStop++
		}
		if exists(w, env.Cwd) {
			nPath++
		}
	}
	question := strings.HasSuffix(s, "?")
	// An existing path as argument means a command, unless the line is clearly prose
	// (three or more request words: "find all pdfs bigger than 10 MB" with a file named "all").
	if nPath > 0 && !question && nStop < 3 {
		return Decision{}
	}
	if nStop >= 2 || question && nStop >= 1 {
		return Decision{AI: true, Hint: true}
	}
	return Decision{}
}

func hasFlag(words []string) bool {
	for _, w := range words[1:] {
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			return true
		}
	}
	return false
}

func looksLikeCode(w string) bool {
	hasDigit := false
	for _, r := range w {
		if unicode.IsDigit(r) {
			hasDigit = true
		} else if !unicode.IsLetter(r) {
			return false
		}
	}
	return hasDigit
}

func exists(p, cwd string) bool {
	p = strings.TrimRight(p, "?,.!")
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	} else if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p, cwd string) bool {
	if cwd == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(cwd, p))
	return err == nil && fi.IsDir()
}

var zshBuiltin = map[string]bool{}

func init() {
	for _, b := range strings.Fields(`cd echo print printf pwd export alias unalias source . exit history fc
		dirs pushd popd jobs fg bg kill wait type whence which where set setopt unsetopt read test [ [[ true false
		eval exec builtin command noglob time typeset local declare let integer float hash rehash umask ulimit
		trap return shift break continue functions autoload zmodload zstyle bindkey zle compdef emulate disown`) {
		zshBuiltin[b] = true
	}
}
