// Package spinner draws zshai's "thinking" line on the terminal while the helper waits
// for the model:
//
//	⠹ thInking
//
// One letter at a time is drawn large (upper case, bold) and the large letter moves
// along the word, so the label waves. The label changes when zshai retries or thinks
// harder. It writes to /dev/tty, never to stdout (the plugin reads stdout), and only
// when the plugin asks for it (ZSHAI_SPINNER holds the colour code).
package spinner

import (
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

var frames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

const (
	tick  = 90 * time.Millisecond
	delay = 80 * time.Millisecond // answers faster than this show no spinner at all
)

type Spinner struct {
	tty   *os.File
	color string
	mu    sync.Mutex
	label []rune
	done  chan struct{}
	wg    sync.WaitGroup
	drawn bool
	sigs  chan os.Signal
}

// Start begins spinning with label, if the plugin enabled it (ZSHAI_SPINNER) and the
// terminal can be opened. A nil *Spinner is valid and does nothing.
func Start(label string) *Spinner {
	color, ok := os.LookupEnv("ZSHAI_SPINNER")
	if !ok {
		return nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	if color == "" {
		color = "90"
	}
	s := &Spinner{tty: tty, color: color, label: []rune(label), done: make(chan struct{}), sigs: make(chan os.Signal, 1)}
	// Ctrl-C while waiting: clear the line and give the cursor back before dying
	signal.Notify(s.sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	s.wg.Add(1)
	go s.loop()
	return s
}

// Set changes the label ("retrying", "thinking harder").
func (s *Spinner) Set(label string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.label = []rune(label)
	s.mu.Unlock()
}

// Stop clears the spinner line and shows the cursor again. Safe to call twice.
func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
		close(s.done)
	}
	s.mu.Unlock()
	s.wg.Wait()
	signal.Stop(s.sigs)
	s.tty.Close()
}

func (s *Spinner) loop() {
	defer s.wg.Done()
	defer func() {
		if s.drawn {
			s.tty.WriteString("\r\x1b[2K\x1b[?25h")
		}
	}()
	select {
	case <-time.After(delay):
	case <-s.done:
		return
	case <-s.sigs:
		os.Exit(130)
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		line := Frame(s.label, i, s.color)
		s.mu.Unlock()
		if !s.drawn {
			line = "\x1b[?25l" + line // hide the real cursor; the spinner is the cursor now
			s.drawn = true
		}
		s.tty.WriteString(line)
		select {
		case <-t.C:
		case <-s.done:
			return
		case <-s.sigs:
			s.tty.WriteString("\r\x1b[2K\x1b[?25h")
			s.drawn = false
			os.Exit(130)
		}
	}
}

// Frame is the line for step i: spinner glyph, then the label with one letter large.
// The wave runs over the letters only, then pauses for a few steps at the end.
func Frame(label []rune, i int, color string) string {
	var letters []int
	for j, r := range label {
		if unicode.IsLetter(r) {
			letters = append(letters, j)
		}
	}
	big := -1
	if n := len(letters); n > 0 {
		if k := i % (n + 3); k < n {
			big = letters[k]
		}
	}
	var sb strings.Builder
	sb.WriteString("\r\x1b[2K  \x1b[" + color + "m")
	sb.WriteRune(frames[i%len(frames)])
	sb.WriteByte(' ')
	for j, r := range label {
		if j == big {
			sb.WriteString("\x1b[1m")
			sb.WriteRune(unicode.ToUpper(r))
			sb.WriteString("\x1b[22m")
		} else {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	sb.WriteString("…\x1b[0m")
	return sb.String()
}
