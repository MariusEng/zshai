package validate

import (
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestQuotedTilde(t *testing.T) {
	for src, want := range map[string]bool{
		`tail -n 5 '~/zshai-test/live/files/app.log'`: true,
		`grep ERROR "~/logs/app.log"`:                 true,
		`tail -n 5 ~/"My Files/app.log"`:              false,
		`tail -n 5 "$HOME/app.log"`:                   false,
		`grep '~' notes.md`:                           false,
		`echo '~/x is your home'`:                     false,
	} {
		s, err := shell.Parse(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(quotedTilde(s.Cmds[0])) > 0; got != want {
			t.Errorf("%s: got %v, want %v", src, got, want)
		}
	}
}
