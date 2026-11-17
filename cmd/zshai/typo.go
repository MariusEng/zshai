package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/msg"
	"github.com/MariusEng/zshai/internal/typo"
)

var zshBuiltins = strings.Fields(`cd echo print printf pwd export alias unalias source exit history fc
	dirs pushd popd jobs fg bg kill wait type whence which where set setopt unsetopt read test true false
	eval exec builtin command noglob time typeset local declare let integer float hash rehash umask ulimit
	trap return shift break continue functions autoload zmodload zstyle bindkey zle compdef emulate disown
	if then else elif fi for while until do done case esac function select repeat`)

// cmdTypo: "zshai typo [--not-found] -- LINE" for the plugin. When the line's command
// word doesn't exist and is close to one that does, it prints zshai_r=( fixed … msg … );
// exit 1 otherwise. --not-found: the shell already said "command not found" (a typo
// inside a pipe or a script line), so the message says so.
func cmdTypo(args []string) int {
	fs := flag.NewFlagSet("typo", flag.ExitOnError)
	notFound := fs.Bool("not-found", false, "")
	whole := fs.String("line", "", "the whole line as typed (--not-found): the fix goes into it")
	_ = fs.Parse(args)
	line := strings.Join(fs.Args(), " ")
	cfg := config.Load()
	if !cfg.Typos || strings.TrimSpace(line) == "" {
		return 1
	}
	extra := map[string]bool{}
	for a := range loadAliases() {
		extra[a] = true
	}
	for f := range loadFunctions() {
		extra[f] = true
	}
	names := pathCommands()
	names = append(names, zshBuiltins...)
	for n := range extra {
		names = append(names, n)
	}
	f, ok := typo.Suggest(line, names, extra)
	if !ok {
		return 1
	}
	fixed := f.Line
	if *notFound && *whole != "" {
		if w, ok := replaceWord(*whole, f.From, f.To); ok {
			fixed = w
		}
	}
	L := msg.New(cfg.Language)
	var m string
	if *notFound {
		m = fmt.Sprintf(t(L, "zshai: mente du «%s»? Den ligger klar i neste linje.", `zshai: did you mean "%s"? It's ready on the next line.`), f.To)
	} else {
		m = fmt.Sprintf(t(L, "zshai: «%s» finnes ikke, mente du «%s»? Enter kjører, eller rett på linja.",
			`zshai: "%s" is not a command, did you mean "%s"? Enter runs it, or edit the line.`), f.From, f.To)
	}
	emit(map[string]string{"status": "typo", "fixed": fixed, "from": f.From, "to": f.To, "msg": m})
	return 0
}

// pathCommands: the executable names on $PATH (what zsh's $commands holds).
func pathCommands() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			n := e.Name()
			if seen[n] || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && (info.Mode()&0o111 != 0 || info.Mode()&os.ModeSymlink != 0) {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// replaceWord: the first whole-word From in line (a command position: start, or after
// | ; & ( or a space) becomes To.
func replaceWord(line, from, to string) (string, bool) {
	for i := 0; i+len(from) <= len(line); i++ {
		if line[i:i+len(from)] != from {
			continue
		}
		if i > 0 && !strings.ContainsRune(" \t|;&(", rune(line[i-1])) {
			continue
		}
		if j := i + len(from); j < len(line) && !strings.ContainsRune(" \t|;&)", rune(line[j])) {
			continue
		}
		return line[:i] + to + line[i+len(from):], true
	}
	return "", false
}
