package validate

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/MariusEng/zshai/internal/shell"
)

// quotedTilde finds '~/x' and "~/x": the shell only expands ~ outside quotes, so the
// command looks for a folder literally named "~".
func quotedTilde(c *shell.Cmd) []string {
	switch c.Name {
	case "echo", "print", "printf", "git":
		return nil // text, not a path (git commit -m '~/…')
	}
	for _, w := range c.Words {
		if w == nil || len(w.Parts) == 0 {
			continue
		}
		lit := ""
		switch p := w.Parts[0].(type) {
		case *syntax.SglQuoted:
			lit = p.Value
		case *syntax.DblQuoted:
			if len(p.Parts) > 0 {
				if l, ok := p.Parts[0].(*syntax.Lit); ok {
					lit = l.Value
				}
			}
		}
		if strings.HasPrefix(lit, "~/") {
			return []string{"~ is not expanded inside quotes: write ~/\"" + strings.TrimPrefix(strings.TrimPrefix(lit, "~"), "/") +
				"\" (quote only the part after ~/), \"$HOME/…\", or a path relative to the cwd"}
		}
	}
	return nil
}
