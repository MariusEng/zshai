// Package azcat holds the Azure CLI command catalog (catalog/az_extract.py) and
// validates az command lines against it (PLAN.md §2.8).
package azcat

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed az.json.gz
var embedded []byte

type Param struct {
	O    []string `json:"o"`
	Req  bool     `json:"req"`
	Ch   []string `json:"ch"`
	Flag bool     `json:"flag"`
}

type Command struct {
	Risk       string  `json:"risk"` // read | change | del
	P          []Param `json:"p"`
	H          string  `json:"h"`
	Yes        bool    `json:"yes"`
	NoWait     bool    `json:"nowait"`
	Deprecated bool    `json:"deprecated"`
	Preview    bool    `json:"preview"`
	WhatIf     string  `json:"whatif"`
}

type Catalog struct {
	Version  string              `json:"version"`
	Groups   map[string]string   `json:"groups"`
	Commands map[string]*Command `json:"commands"`
	Source   string              `json:"-"`
}

var (
	once sync.Once
	cat  *Catalog
	err  error
)

// LocalPath is where a user's own extract lives (it overrides the embedded baseline).
func LocalPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zshai", "az.json")
}

// Get loads the catalog once.
func Get() (*Catalog, error) {
	once.Do(func() {
		if b, e := os.ReadFile(LocalPath()); e == nil {
			cat, err = parse(b, LocalPath())
			if err == nil {
				return
			}
		}
		if len(embedded) == 0 {
			err = fmt.Errorf("no az catalog")
			return
		}
		zr, e := gzip.NewReader(bytes.NewReader(embedded))
		if e != nil {
			err = e
			return
		}
		b, e := io.ReadAll(zr)
		if e != nil {
			err = e
			return
		}
		cat, err = parse(b, "embedded")
	})
	return cat, err
}

func parse(b []byte, src string) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.Source = src
	return &c, nil
}

// Global options every az command accepts.
var globals = map[string]bool{
	"--output": true, "-o": true, "--query": true, "--debug": true, "--verbose": true,
	"--only-show-errors": true, "--help": true, "-h": true, "--subscription": true,
}

var globalTakesValue = map[string]bool{"--output": true, "-o": true, "--query": true, "--subscription": true}

// Resolve finds the longest command path at the start of args (args exclude "az").
func (c *Catalog) Resolve(args []string) (string, *Command, []string) {
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, a)
	}
	for n := len(words); n > 0; n-- {
		name := strings.Join(words[:n], " ")
		if cmd, ok := c.Commands[name]; ok {
			return name, cmd, args[n:]
		}
	}
	return "", nil, args
}

// Group reports whether name is a command group.
func (c *Catalog) Group(name string) bool { _, ok := c.Groups[name]; return ok }

// Defaults are configured values (az config / ~/.azure/config [defaults]).
type Defaults struct{ Group, Location string }

// Validate returns problems with an az invocation (args exclude "az").
func (c *Catalog) Validate(args []string, d Defaults) []string {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "--version" || args[0] == "version" || args[0] == "-h" || args[0] == "--help" {
		return nil
	}
	name, cmd, rest := c.Resolve(args)
	if cmd == nil {
		path := leadingWords(args)
		// a group alone (az vm -h) or "az group" listing help
		if c.Group(strings.Join(path, " ")) {
			return nil
		}
		return []string{fmt.Sprintf("az %s: no such command in az %s%s", strings.Join(path, " "), c.Version, c.suggest(path))}
	}
	var probs []string
	known := map[string]*Param{}
	for i := range cmd.P {
		for _, o := range cmd.P[i].O {
			known[o] = &cmd.P[i]
		}
	}
	seen := map[*Param]bool{}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		opt, val, hasEq := strings.Cut(a, "=")
		if globals[opt] {
			if globalTakesValue[opt] && !hasEq {
				i++
			}
			continue
		}
		p := known[opt]
		if p == nil {
			probs = append(probs, fmt.Sprintf("az %s: unknown option %s%s", name, opt, closest(opt, known)))
			continue
		}
		seen[p] = true
		if p.Flag {
			continue
		}
		if !hasEq && i+1 < len(rest) {
			val = rest[i+1]
			i++
		}
		if len(p.Ch) > 0 && val != "" && !strings.ContainsAny(val, "$`") {
			okv := false
			for _, ch := range p.Ch {
				if strings.EqualFold(ch, val) {
					okv = true
					break
				}
			}
			if !okv {
				probs = append(probs, fmt.Sprintf("az %s %s: %q is not one of %s", name, opt, val, strings.Join(p.Ch, ", ")))
			}
		}
	}
	for i := range cmd.P {
		p := &cmd.P[i]
		if !p.Req || seen[p] {
			continue
		}
		if has(p.O, "--resource-group") && d.Group != "" || has(p.O, "--location") && d.Location != "" {
			continue
		}
		// --ids can replace name/group on many commands
		if (has(p.O, "--name") || has(p.O, "--resource-group")) && seenOpt(seen, known, "--ids") {
			continue
		}
		probs = append(probs, fmt.Sprintf("az %s: missing required %s", name, p.O[0]))
	}
	return probs
}

func seenOpt(seen map[*Param]bool, known map[string]*Param, opt string) bool {
	p := known[opt]
	return p != nil && seen[p]
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func leadingWords(args []string) []string {
	var w []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		w = append(w, a)
	}
	return w
}

func (c *Catalog) suggest(path []string) string {
	// find the deepest existing group and list a few of its commands
	for n := len(path) - 1; n > 0; n-- {
		g := strings.Join(path[:n], " ")
		if c.Group(g) {
			var subs []string
			prefix := g + " "
			for name := range c.Commands {
				if strings.HasPrefix(name, prefix) && !strings.Contains(name[len(prefix):], " ") {
					subs = append(subs, name[len(prefix):])
				}
			}
			sort.Strings(subs)
			if len(subs) > 12 {
				subs = subs[:12]
			}
			return fmt.Sprintf(" (az %s has: %s)", g, strings.Join(subs, ", "))
		}
	}
	return ""
}

func closest(opt string, known map[string]*Param) string {
	best, bd := "", 4
	for o := range known {
		if d := lev(opt, o); d < bd {
			best, bd = o, d
		}
	}
	if best == "" {
		return ""
	}
	return " (did you mean " + best + "?)"
}

func lev(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// ExtractScript is az_extract.py, so `zshai az-catalog` can re-extract on the user's machine.
//
//go:embed az_extract.py
var ExtractScript []byte
