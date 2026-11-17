// Package typo suggests the command a mistyped first word was meant to be ("rn" -> "rm",
// "sl" -> "ls", "gti" -> "git", "cd.." -> "cd .."). No model: a keyboard-aware edit
// distance over the commands, aliases and functions the shell actually has, so it
// answers in a few milliseconds. The plugin puts the fixed line back in the prompt; it
// never runs anything by itself.
package typo

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Fix: the line with its command word corrected.
type Fix struct {
	Line string // the whole corrected line
	From string // the word as typed
	To   string // what it became ("cd .." for "cd..")
}

// Common commands, most used first: a typo is matched to these before anything else on
// PATH, and a looser match (a whole edit away) is only accepted for these, the user's
// aliases and functions. Otherwise "rn" could become some rarely used tool.
var Common = strings.Fields(`
	ls cd rm mv cp cat grep git mkdir less echo sudo vim vi nano code find touch chmod chown
	ssh scp ps kill top htop df du tar unzip zip curl wget ping head tail sort uniq wc
	diff man which clear exit history pwd ln make python python3 pip pip3 node npm npx
	go cargo docker kubectl brew apt systemctl journalctl open sed awk tree rg fd jq
	export source alias rmdir file stat date whoami tmux zshai az gh bat eza
	`)

var rank = map[string]int{}

func init() {
	for i, c := range Common {
		rank[c] = i + 1
	}
}

// Plain-language first words (English, Norwegian): a line starting with one is a request
// for the model, never a typo ("vis filene" is not "vi filene").
var words = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		show list make create delete remove give count tell convert compress extract download rename
		move copy check start stop restart install update search what how where why when who which
		can could please find get set run open print help explain fix change sort clean count calculate
		is are does do the my all
		vis lag finn hvor hva hvordan hvorfor hvem slett fjern flytt kopier endre gi tell sjekk
		start stopp installer oppdater søk kan kunne skriv les åpne pakk komprimer last hent rydd
		sorter regn beregn forklar hjelp gjør er har min mine alle
	`) {
		words[w] = true
	}
}

// Suggest corrects the line's command word (after sudo/doas), or returns false. names
// are the commands, aliases, functions and builtins the shell knows; extra marks the
// user's own aliases and functions (as trusted as Common). Only the command word changes;
// the arguments are left as typed.
func Suggest(line string, names []string, extra map[string]bool) (Fix, bool) {
	lead := len(line) - len(strings.TrimLeft(line, " \t"))
	rest := line[lead:]
	prefix := ""
	if w, after, ok := strings.Cut(rest, " "); ok && (w == "sudo" || w == "doas") {
		trim := strings.TrimLeft(after, " ")
		prefix = rest[:len(rest)-len(trim)]
		rest = trim
	}
	word, args, _ := strings.Cut(rest, " ")
	if word == "" || strings.ContainsAny(word, "/=$`'\"\\*?[]{}()<>|&;~") && !glued(word) {
		return Fix{}, false
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	if known[word] {
		return Fix{}, false
	}
	if words[strings.ToLower(word)] || looksLikeSentence(args) {
		return Fix{}, false
	}
	to := ""
	// "cd.." "ls-la" "cd~": a command glued to its first argument
	if g := split(word, known); g != "" {
		to = g
	} else {
		to = closest(word, known, extra)
	}
	if to == "" {
		return Fix{}, false
	}
	out := line[:lead] + prefix + to
	if args != "" {
		out += " " + args
	}
	return Fix{Line: out, From: word, To: to}, true
}

func glued(w string) bool { return strings.IndexAny(w, ".-~/") > 0 }

func split(w string, known map[string]bool) string {
	i := strings.IndexAny(w, ".-~/")
	if i <= 0 {
		return ""
	}
	if c := w[:i]; known[c] && rank[c] > 0 {
		return c + " " + w[i:]
	}
	return ""
}

// looksLikeSentence: the rest of the line reads as words of a request, not arguments
// (it has small words like "me", "all", "the": "apt install jq" is arguments).
func looksLikeSentence(args string) bool {
	f := strings.Fields(args)
	if len(f) < 2 {
		return false
	}
	n := 0
	for _, w := range f {
		if strings.ContainsAny(w, "-./~*$=") {
			return false
		}
		if stop[strings.ToLower(w)] {
			n++
		}
	}
	return n > 0
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the of in on to for with from me i you it this that and or
		all my your what
		en et ei den det de i på til for med fra meg jeg du og eller som alle min mine hva`) {
		stop[w] = true
	}
}

type cand struct {
	name  string
	score float64
}

func closest(word string, known map[string]bool, extra map[string]bool) string {
	n := utf8.RuneCountInString(word)
	if n > 30 {
		return ""
	}
	// how far a fix may be: one slip of a finger for short words
	limit := 1.0
	switch {
	case n <= 2:
		limit = 0.5
	case n >= 6:
		limit = 1.5
	}
	var cs []cand
	for name := range known {
		d := float64(utf8.RuneCountInString(name) - n)
		if d > 2 || d < -2 || strings.HasPrefix(name, "_") {
			continue
		}
		s := Distance(word, name)
		if s > limit {
			continue
		}
		// a whole edit away: only to commands people use, not to any binary on PATH
		if s > 0.5 && rank[name] == 0 && !extra[name] {
			continue
		}
		cs = append(cs, cand{name, s})
	}
	if len(cs) == 0 {
		return ""
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.score != b.score {
			return a.score < b.score
		}
		ra, rb := pri(a.name, extra), pri(b.name, extra)
		if ra != rb {
			return ra < rb
		}
		return a.name < b.name
	})
	// two different fixes that are equally good and equally common: don't guess
	if len(cs) > 1 && cs[0].score == cs[1].score && pri(cs[0].name, extra) == pri(cs[1].name, extra) {
		return ""
	}
	return cs[0].name
}

func pri(name string, extra map[string]bool) int {
	if r := rank[name]; r > 0 {
		return r
	}
	if extra[name] {
		return 1000
	}
	return 2000
}

// Distance: Damerau-Levenshtein (adjacent swaps) where slips of a finger cost less:
// a neighbouring key or a swap 0.5, a doubled or neighbouring extra letter 0.5, only
// upper/lower case 0.1; anything else 1.
func Distance(a, b string) float64 {
	x, y := []rune(a), []rune(b)
	m, n := len(x), len(y)
	d := make([][]float64, m+1)
	for i := range d {
		d[i] = make([]float64, n+1)
		d[i][0] = float64(i)
	}
	for j := 0; j <= n; j++ {
		d[0][j] = float64(j)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			sub := subCost(x[i-1], y[j-1])
			del := 1.0 // a letter typed that isn't in the command
			if extraKey(x, i-1) {
				del = 0.5
			}
			v := min(d[i-1][j]+del, d[i][j-1]+1, d[i-1][j-1]+sub)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				v = min(v, d[i-2][j-2]+0.5)
			}
			d[i][j] = v
		}
	}
	return d[m][n]
}

func subCost(a, b rune) float64 {
	if a == b {
		return 0
	}
	if unicode.ToLower(a) == unicode.ToLower(b) {
		return 0.1
	}
	if near(unicode.ToLower(a), unicode.ToLower(b)) {
		return 0.5
	}
	return 1
}

// extraKey: x[i] doubles the letter before or after it, or sits next to one of them on
// the keyboard (a finger hitting two keys).
func extraKey(x []rune, i int) bool {
	c := unicode.ToLower(x[i])
	for _, k := range []int{i - 1, i + 1} {
		if k >= 0 && k < len(x) {
			o := unicode.ToLower(x[k])
			if o == c || near(o, c) {
				return true
			}
		}
	}
	return false
}

// QWERTY rows (Norwegian keyboards have the same letters in the same places).
var rows = []string{"1234567890", "qwertyuiop", "asdfghjkl", "zxcvbnm"}

type pos struct{ r, c int }

var keys = map[rune]pos{}

func init() {
	for r, row := range rows {
		for c, k := range row {
			keys[k] = pos{r, c}
		}
	}
}

func near(a, b rune) bool {
	p, ok1 := keys[a]
	q, ok2 := keys[b]
	if !ok1 || !ok2 {
		return false
	}
	dr, dc := p.r-q.r, p.c-q.c
	if dr == 0 {
		return dc == 1 || dc == -1
	}
	// the row below is shifted half a key right: q's neighbours below are a and s... w's are a, s
	if dr == -1 { // a is above b
		return dc == 0 || dc == 1
	}
	if dr == 1 { // a is below b
		return dc == 0 || dc == -1
	}
	return false
}
