package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/localmodel"
	"github.com/MariusEng/zshai/internal/msg"
	"github.com/MariusEng/zshai/internal/sandbox"
	"github.com/MariusEng/zshai/internal/store"
)

// debugMeta: "zshai: debug [n | id]" writes everything zshai knows about the last n
// requests (default 1) to one Markdown file, to send to whoever helps debugging: setup,
// the system prompt, every model call with its context and raw reply, the problems sent
// back, every test run and the outcome. On macOS the file is also put on the clipboard.
func debugMeta(L msg.L, cfg config.Config, rest string) string {
	if !cfg.Log {
		return t(L, "Loggen er slått av (\"log\": false i config), så det er ingenting å eksportere.\n",
			"The log is off (\"log\": false in the config), so there is nothing to export.\n")
	}
	var uids []string
	n := 1
	if rest != "" {
		if v, err := strconv.Atoi(rest); err == nil && v > 0 {
			n = min(v, 20)
		} else if r, _, _, ok := store.Detail(rest); ok {
			uids = []string{r.UID}
		} else {
			return t(L, "Fant ingen forespørsel med id ", "No request with id ") + rest + "\n"
		}
	}
	if uids == nil {
		for _, r := range store.Last(n, false) {
			uids = append(uids, r.UID)
		}
		slices.Reverse(uids) // oldest first
	}
	if len(uids) == 0 {
		return t(L, "Loggen er tom.\n", "The log is empty.\n")
	}
	report := debugReport(cfg, uids)

	dir := filepath.Join(config.StateDir(), "debug")
	if home, err := os.UserHomeDir(); err == nil {
		if st, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && st.IsDir() {
			dir = filepath.Join(home, "Downloads")
		}
	}
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "zshai-debug-"+time.Now().Format("20060102-150405")+".md")
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		return "zshai: " + err.Error() + "\n"
	}
	clip := ""
	if runtime.GOOS == "darwin" && len(report) < 2<<20 {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(report)
		if c.Run() == nil {
			clip = t(L, " Den ligger også på utklippstavla (⌘V).", " It is also on the clipboard (⌘V).")
		}
	}
	kb := (len(report) + 1023) / 1024
	out := fmt.Sprintf(t(L, "Feilsøkingsfil med %d forespørsel(er), %d KB: %s\n", "Debug file with %d request(s), %d KB: %s\n"), len(uids), kb, path)
	if clip != "" {
		out += "  " + strings.TrimSpace(clip) + "\n"
	}
	return out + t(L, "  Den har mappeinnhold, filnavn og maskinnavn fra konteksten: se over før du deler den.\n",
		"  It has folder listings, file names and host names from the context: look it over before sharing.\n")
}

// fence picks a code fence longer than any backtick run in s.
func fence(s string) string {
	f := "```"
	for strings.Contains(s, f) {
		f += "`"
	}
	return f
}

func block(sb *strings.Builder, lang, s string) {
	f := fence(s)
	fmt.Fprintf(sb, "%s%s\n%s\n%s\n", f, lang, strings.TrimRight(s, "\n"), f)
}

func debugReport(cfg config.Config, uids []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# zshai debug report\n\n")
	fmt.Fprintf(&sb, "- created: %s\n", time.Now().Format("2006-01-02 15:04:05 -0700"))
	fmt.Fprintf(&sb, "- zshai: %s\n", versionLine())
	if pv := os.Getenv("ZSHAI_PLUGIN_VERSION"); pv != "" {
		fmt.Fprintf(&sb, "- plugin: %s\n", pv)
	}
	if zv := os.Getenv("ZSHAI_ZSH_VERSION"); zv != "" {
		fmt.Fprintf(&sb, "- zsh: %s\n", zv)
	}
	if localmodel.IsLocal(cfg.Server) {
		fmt.Fprintf(&sb, "- model: %s\n", localmodel.Status())
	} else {
		fmt.Fprintf(&sb, "- model: %s @ %s\n", cfg.Model, cfg.Server)
	}
	sbx := sandbox.Kind()
	if sbx == "" {
		sbx = "none"
	}
	fmt.Fprintf(&sb, "- test runs: %v (sandbox: %s)\n", cfg.TestRun, sbx)
	c := cfg
	if c.APIKey != "" {
		c.APIKey = "(hidden)"
	}
	if c.Escalate.APIKey != "" {
		c.Escalate.APIKey = "(hidden)"
	}
	cj, _ := json.MarshalIndent(c, "", "  ")
	sb.WriteString("\n<details><summary>config</summary>\n\n")
	block(&sb, "json", string(cj))
	sb.WriteString("\n</details>\n")

	lastSys := ""
	for i, uid := range uids {
		r, calls, effects, ok := store.Detail(uid)
		if !ok {
			continue
		}
		info := store.Info(uid)
		fmt.Fprintf(&sb, "\n---\n\n## %d. %s\n\n", i+1, r.Request)
		fmt.Fprintf(&sb, "- id: `%s` · %s\n- cwd: `%s`\n", r.UID, r.Started, r.Cwd)
		fmt.Fprintf(&sb, "- outcome: **%s** · mode: %s · risk: %s", r.Outcome, r.Mode, r.Risk)
		if r.Exit != nil {
			fmt.Fprintf(&sb, " · exit: %d", *r.Exit)
		}
		fmt.Fprintf(&sb, "\n- model: %s · model time %.2f s · total %.2f s\n", info.Model, r.ModelSeconds, info.TotalSeconds)
		if info.FixOf != "" {
			fmt.Fprintf(&sb, "- fix of: `%s`\n", info.FixOf)
		}
		if info.Error != "" {
			fmt.Fprintf(&sb, "- error: %s\n", info.Error)
		}
		if r.Script != "" {
			sb.WriteString("\nFinal command:\n\n")
			block(&sb, "zsh", r.Script)
		}
		if len(info.Problems) > 0 {
			sb.WriteString("\nProblems found by zshai's checks (all attempts):\n\n")
			for _, p := range info.Problems {
				fmt.Fprintf(&sb, "- %s\n", p)
			}
		}
		if info.SystemPrompt != "" && info.SystemPrompt != lastSys {
			lastSys = info.SystemPrompt
			sb.WriteString("\n<details><summary>system prompt</summary>\n\n")
			block(&sb, "", info.SystemPrompt)
			sb.WriteString("\n</details>\n")
		}
		for j, x := range calls {
			fmt.Fprintf(&sb, "\n### Model call %d (%.2f s)\n\n", j+1, x.Seconds)
			if x.Model != "" && x.Model != info.Model {
				fmt.Fprintf(&sb, "Model: **%s** (escalated)\n\n", x.Model)
			}
			if x.System != "" {
				sb.WriteString("<details><summary>system prompt for this call</summary>\n\n")
				block(&sb, "", x.System)
				sb.WriteString("\n</details>\n\n")
			}
			if x.Error != "" {
				fmt.Fprintf(&sb, "Error: %s\n\n", x.Error)
			}
			sb.WriteString("Sent:\n\n")
			block(&sb, "", x.Prompt)
			sb.WriteString("\nReply:\n\n")
			block(&sb, "", x.Response)
		}
		for j, x := range store.Tests(uid) {
			fmt.Fprintf(&sb, "\n### Test run %d (%.2f s)\n\n", j+1, x.Seconds)
			fmt.Fprintf(&sb, "- exit: %d · timed out: %v · blocked by sandbox: %v\n", x.Exit, x.TimedOut, x.Denied)
			if x.Summary != "" {
				fmt.Fprintf(&sb, "- shown: %s\n", x.Summary)
			}
			for _, e := range x.Effects {
				fmt.Fprintf(&sb, "- effect: %s\n", e)
			}
			for _, p := range x.Problems {
				fmt.Fprintf(&sb, "- problem: %s\n", p)
			}
			if x.Command != "" {
				sb.WriteString("\n")
				block(&sb, "zsh", x.Command)
			}
			if x.Stderr != "" {
				sb.WriteString("\nstderr:\n\n")
				block(&sb, "", x.Stderr)
			}
			if x.Stdout != "" {
				sb.WriteString("\nstdout:\n\n")
				block(&sb, "", x.Stdout)
			}
		}
		if len(effects) > 0 {
			sb.WriteString("\n### Effects after running\n\n")
			for _, e := range effects {
				fmt.Fprintf(&sb, "- %s %s", e.Action, e.Path)
				if e.Dest != "" {
					fmt.Fprintf(&sb, " → %s", e.Dest)
				}
				fmt.Fprintf(&sb, " (%s)\n", e.Via)
			}
		}
	}
	return sb.String()
}
