package spinner

import (
	"strings"
	"testing"
)

func TestFrameWaves(t *testing.T) {
	strip := func(s string) string {
		for _, c := range []string{"\r", "\x1b[2K", "\x1b[90m", "\x1b[1m", "\x1b[22m", "\x1b[0m"} {
			s = strings.ReplaceAll(s, c, "")
		}
		return s
	}
	l := []rune("prøver igjen")
	if got := strip(Frame(l, 0, "90")); got != "  ⠋ Prøver igjen…" {
		t.Errorf("frame 0: %q", got)
	}
	// the space is skipped: letter 6 is the i of "igjen"
	if got := strip(Frame(l, 6, "90")); got != "  ⠦ prøver Igjen…" {
		t.Errorf("frame 6: %q", got)
	}
	// a pause after the last letter: nothing is large
	if got := strip(Frame(l, 11, "90")); got != "  ⠙ prøver igjen…" {
		t.Errorf("frame 11: %q", got)
	}
}
