package main

import (
	"encoding/json"
	"fmt"
	"github.com/MariusEng/zshai/internal/contextinfo"
	"github.com/MariusEng/zshai/internal/llm"
	"github.com/MariusEng/zshai/internal/prompt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/azcat"
	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/journal"
	"github.com/MariusEng/zshai/internal/localmodel"
	"github.com/MariusEng/zshai/internal/msg"
	"github.com/MariusEng/zshai/internal/risk"
	"github.com/MariusEng/zshai/internal/sandbox"
	"github.com/MariusEng/zshai/internal/shell"
	"github.com/MariusEng/zshai/internal/store"
	"github.com/MariusEng/zshai/internal/theme"
	"github.com/MariusEng/zshai/internal/trash"
)

var azExtractPy = azcat.ExtractScript

type trashMove struct{ From, To string }

func toMoved(ms []trashMove) []trash.Moved {
	out := make([]trash.Moved, len(ms))
	for i, m := range ms {
		out[i] = trash.Moved{From: m.From, To: m.To}
	}
	return out
}

func trashPut(paths []string) ([]trash.Moved, error) { return trash.Put(paths) }

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// gitBackup saves HEAD, a stash snapshot of uncommitted changes and branch tips
// under refs/zshai/backup/<id> before a git command that can lose work (PLAN.md §2.7).
func gitBackup(t *turnState) (journal.Entry, bool) {
	root, err := git(t.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return journal.Entry{}, false
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return journal.Entry{}, false
	}
	e := journal.Entry{ID: t.ID, Time: time.Now(), Kind: "git-backup", Request: t.Request, Command: t.Command, Repo: root, Head: head}
	ref := "refs/zshai/backup/" + t.ID
	if _, err := git(root, "update-ref", ref+"/head", head); err == nil {
		e.Ref = ref
	}
	if stash, err := git(root, "stash", "create", "zshai backup "+t.ID); err == nil && stash != "" {
		e.Stash = stash
		_, _ = git(root, "update-ref", ref+"/stash", stash)
	}
	// branch -D: remember the tips of the branches being deleted
	if s, err := shell.Parse(t.Command, nil); err == nil {
		for _, c := range s.Cmds {
			if c.Name != "git" {
				continue
			}
			sub, args := risk.GitSub(c.Args)
			if sub != "branch" {
				continue
			}
			for _, br := range shell.Operands(args) {
				if sha, err := git(root, "rev-parse", "refs/heads/"+br); err == nil {
					_, _ = git(root, "update-ref", ref+"/branch/"+br, sha)
					e.Branch += br + "=" + sha + " "
				}
			}
		}
	}
	pruneBackups(root)
	return e, true
}

// pruneBackups removes zshai backup refs older than 30 days.
func pruneBackups(root string) {
	out, err := git(root, "for-each-ref", "--format=%(refname)", "refs/zshai/backup/")
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -30).Format("20060102")
	for _, ref := range strings.Fields(out) {
		parts := strings.Split(strings.TrimPrefix(ref, "refs/zshai/backup/"), "-")
		if len(parts) > 0 && parts[0] < cutoff {
			_, _ = git(root, "update-ref", "-d", ref)
		}
	}
}

// undo reverses the newest journal entry.
func undo(L msg.L) string {
	e, ok := journal.LastUndoable()
	if !ok {
		return t(L, "Det er ingenting å angre.", "There is nothing to undo.")
	}
	var out []string
	switch e.Kind {
	case "trash":
		n, problems := trash.Restore(e.Moves)
		out = append(out, t(L, fmt.Sprintf("La tilbake %d av %d fra papirkurven.", n, len(e.Moves)), fmt.Sprintf("Restored %d of %d from the trash.", n, len(e.Moves))))
		out = append(out, problems...)
	case "move":
		n := 0
		for i := len(e.Moves) - 1; i >= 0; i-- {
			m := e.Moves[i]
			if _, err := os.Lstat(m.From); err == nil {
				out = append(out, m.From+t(L, ": finnes allerede, ikke flyttet tilbake", ": already exists, not moved back"))
				continue
			}
			if err := os.Rename(m.To, m.From); err != nil {
				out = append(out, err.Error())
				continue
			}
			n++
		}
		out = append([]string{t(L, fmt.Sprintf("Flyttet %d av %d tilbake.", n, len(e.Moves)), fmt.Sprintf("Moved %d of %d back.", n, len(e.Moves)))}, out...)
	case "git-backup":
		if e.Branch != "" {
			for _, kv := range strings.Fields(e.Branch) {
				br, sha, _ := strings.Cut(kv, "=")
				if _, err := git(e.Repo, "rev-parse", "--verify", "refs/heads/"+br); err == nil {
					continue
				}
				if _, err := git(e.Repo, "branch", br, sha); err == nil {
					out = append(out, t(L, "Gjenopprettet branch ", "Restored branch ")+br)
				}
			}
		}
		cur, _ := git(e.Repo, "rev-parse", "HEAD")
		if cur != e.Head {
			if _, err := git(e.Repo, "reset", "--keep", e.Head); err != nil {
				out = append(out, t(L, "Kunne ikke flytte HEAD tilbake (", "Couldn't move HEAD back (")+err.Error()+t(L, "). Backupen ligger i ", "). The backup is in ")+e.Ref)
			} else {
				out = append(out, t(L, "HEAD er tilbake på ", "HEAD is back at ")+e.Head[:min(8, len(e.Head))])
			}
		}
		if e.Stash != "" {
			if _, err := git(e.Repo, "stash", "apply", e.Stash); err != nil {
				out = append(out, t(L, "Endringene ligger i ", "The changes are in ")+e.Stash+t(L, " (git stash apply "+e.Stash+")", " (git stash apply "+e.Stash+")"))
			} else {
				out = append(out, t(L, "Ulagrede endringer er lagt tilbake.", "Uncommitted changes are restored."))
			}
		}
		if len(out) == 0 {
			out = append(out, t(L, "Ingenting å gjenopprette.", "Nothing to restore."))
		}
	}
	_ = journal.MarkUndone(e)
	var eff []store.Effect
	for _, m := range e.Moves {
		eff = append(eff, store.Effect{Action: "restored", Path: m.From, Dest: m.To, Via: "undo"})
	}
	if e.Kind == "git-backup" {
		eff = append(eff, store.Effect{Action: "restored", Path: e.Repo, Dest: e.Ref, Via: "undo"})
	}
	store.AddEffects(e.ID, eff)
	return strings.Join(out, "\n")
}

func t(L msg.L, no, en string) string {
	if L.No {
		return no
	}
	return en
}

// meta handles zshai's own "zshai: …" commands (PLAN.md §1.4); no model involved.
// ok is false when the line is not one of them: then it is a request for the model
// ("zshai: hvor stor er denne mappa?").
func meta(line string) (out string, ok bool) {
	cfg := config.Load()
	L := msg.New(cfg.Language)
	line = strings.TrimSpace(line)
	for _, p := range []string{"zshai:", "zai:", "zshai"} { // zai: is the short form
		if rest, ok := strings.CutPrefix(line, p); ok {
			line = strings.TrimSpace(rest)
			break
		}
	}
	word, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	word = strings.ToLower(word)
	_, restNum := strconv.Atoi(rest)
	switch {
	case (word == "" || word == "hjelp" || word == "help") && rest == "",
		(word == "angre" || word == "undo" || word == "status" || word == "stats" || word == "statistikk" || word == "versjon" || word == "version" ||
			word == "config" || word == "konfig" || word == "innstillinger" || word == "settings") && rest == "",
		// "config fixAttempts=2", "config typos av", "config fixAttempts": a setting, not a request
		(word == "config" || word == "konfig" || word == "innstillinger" || word == "settings") && configArg.MatchString(rest),
		(word == "husk" || word == "remember"),
		word == "språk" || word == "sprak" || word == "lang" || word == "language" || word == "lng",
		(word == "glem" || word == "forget") && (rest == "" || rest == "alt" || rest == "all" || restNum == nil),
		(word == "siste" || word == "last" || word == "logg" || word == "log") && (rest == "" || restNum == nil),
		word == "auto" && (rest == "" || rest == "på" || rest == "av" || rest == "on" || rest == "off"),
		word == "theme" || word == "tema",
		word == "debug" || word == "feilsøk" || word == "feilsok" || word == "feilsøking",
		(word == "model" || word == "modell") && (rest == "" || len(strings.Fields(rest)) == 1),
		word == "prompt" || word == "systemprompt" || word == "system-prompt" || word == "ledetekst" ||
			(word == "system" && (rest == "prompt" || strings.HasPrefix(rest, "prompt "))):
	default:
		return "", false
	}
	return metaRun(L, cfg, word, rest), true
}

// editMarker starts meta output that means "open this file in the editor" (exit 4).
const editMarker = "\x00edit\x00"

func metaRun(L msg.L, cfg config.Config, word, rest string) string {
	if word == "system" {
		word, rest = "prompt", strings.TrimSpace(strings.TrimPrefix(rest, "prompt"))
	}
	switch word {
	case "prompt", "systemprompt", "system-prompt", "ledetekst":
		return promptMeta(L, cfg, rest)
	case "debug", "feilsøk", "feilsok", "feilsøking":
		return debugMeta(L, cfg, rest)
	case "model", "modell":
		if rest == "" || rest == "list" || rest == "liste" {
			return localmodel.List()
		}
		name := strings.ToLower(rest)
		if _, ok := localmodel.Installed()[name]; !ok {
			if _, known := localmodel.Lookup(name); !known {
				return t(L, "Ukjent modell. Velg 1.5b eller 3b.\n", "Unknown model. Choose 1.5b or 3b.\n")
			}
			return fmt.Sprintf(t(L, "%s er ikke installert. Last den ned med:  zshai model install %s\n",
				"%s is not installed. Download it with:  zshai model install %s\n"), name, name)
		}
		cfg.LocalModel = name
		if err := config.Save(cfg); err != nil {
			return err.Error() + "\n"
		}
		localmodel.Stop()
		return t(L, "Bruker ", "Using ") + name + t(L, " fra neste forespørsel.\n", " from the next request.\n")
	case "", "hjelp", "help":
		return t(L, `zshai: skriv «zshai:» og så hva du vil gjøre, på norsk eller engelsk. Andre linjer er vanlige kommandoer.
  zshai: hvor stor er denne mappa?       gjør det (spør først hvis noe endres)
  zshai: hvordan lister jeg bare mapper? viser kommandoen og forklarer den, kjører ikke
  zshai: forklar tar -xzvf a.tgz         forklarer en kommando
  zai: …      kort for «zshai: …»       ?tekst      også kort for «zshai: tekst»  ,kommando   kjør som vanlig kommando
  zshai: angre            angre siste flytt/sletting/git-reset
  zshai: siste [n]        de siste forespørslene
  zshai: status           modell, språk, pakkesystem, az
  zshai: config           vis innstillingsfila (config fixAttempts=2 endrer en, config typos viser en)
  zshai: modell [3b]      vis modellene, eller bytt til en annen størrelse
  zshai: debug [n]        lag en feilsøkingsfil av de siste n forespørslene (alt modellen fikk og svarte, testkjøringer)
  zshai: prompt           rediger systemprompten (prompt vis · prompt tilbakestill)
  zshai: språk engelsk    bytt språk (engelsk / norsk)
  zshai: husk <tekst>     notat som sendes med (f.eks. «prosjektene ligger i ~/git»)
  zshai: glem <n|alt>     fjern notater
  zshai: tema [navn]      vis og velg fargetema
  zshai: auto på|av       send også vanlig tekst uten «zshai:» til zshai (nå: `+onOff(L, cfg.AutoDetect)+`)
  zshai: skrivefeil på|av foreslå riktig kommando ved skrivefeil, som rn → rm (nå: `+onOff(L, cfg.Typos)+`)
`, `zshai: type "zshai:" and what you want to do, in English or Norwegian. Other lines are normal commands.
  zshai: how big is this folder?         does it (asks first if anything changes)
  zshai: how do I list only folders?     shows the command and explains it, doesn't run it
  zshai: explain tar -xzvf a.tgz         explains a command
  zai: …      short for "zshai: …"      ?text       also short for "zshai: text"  ,command    run as a normal command
  zshai: undo             undo the last move/delete/git reset
  zshai: last [n]         the latest requests
  zshai: status           model, language, package manager, az
  zshai: config           show the settings file (config fixAttempts=2 changes one, config typos shows one)
  zshai: model [3b]       list the models, or switch to another size
  zshai: debug [n]        write a debug file for the last n requests (everything the model got and answered, test runs)
  zshai: prompt           edit the system prompt (prompt show · prompt reset)
  zshai: lang norwegian   switch language (english / norwegian)
  zshai: remember <text>  a note sent with requests ("my projects are in ~/git")
  zshai: forget <n|all>   remove notes
  zshai: theme [name]     show and pick a colour theme
  zshai: auto on|off      also send plain text without "zshai:" to zshai (now: `+onOff(L, cfg.AutoDetect)+`)
  zshai: typos on|off     suggest the right command for a typo, like rn → rm (now: `+onOff(L, cfg.Typos)+`)
`)
	case "theme", "tema":
		if rest == "" {
			return themeList(L, cfg)
		}
		name := themeArg(rest)
		if name == "" {
			return t(L, "Ukjent tema. Velg et av: ", "Unknown theme. Pick one of: ") + strings.Join(theme.Names(), ", ") + "\n"
		}
		cfg.Theme = name
		_ = config.Save(cfg)
		return t(L, "Tema: ", "Theme: ") + name + "\n"
	case "auto":
		switch rest {
		case "på", "on":
			cfg.AutoDetect = true
		case "av", "off":
			cfg.AutoDetect = false
		default:
			return "zshai auto: " + onOff(L, cfg.AutoDetect) + "\n"
		}
		_ = config.Save(cfg)
		return t(L, "Automatisk gjenkjenning: ", "Auto-detect: ") + onOff(L, cfg.AutoDetect) + "\n"
	case "typos", "typo", "skrivefeil":
		switch rest {
		case "på", "on":
			cfg.Typos = true
		case "av", "off":
			cfg.Typos = false
		default:
			return t(L, "Skrivefeilforslag: ", "Typo suggestions: ") + onOff(L, cfg.Typos) + "\n"
		}
		_ = config.Save(cfg)
		return t(L, "Skrivefeilforslag: ", "Typo suggestions: ") + onOff(L, cfg.Typos) + "\n"
	case "angre", "undo":
		return undo(L) + "\n"
	case "språk", "sprak", "lang", "language":
		lang := langArg(rest)
		if lang == "" {
			cur := t(L, "Språk: norsk", "Language: English")
			return cur + t(L, ". Bytt med «zshai: språk engelsk» eller «zshai: språk norsk».\n",
				`. Switch with "zshai: lang norwegian" or "zshai: lang english".`+"\n")
		}
		cfg.Language = lang
		_ = config.Save(cfg)
		return t(msg.New(lang), "Språk: norsk\n", "Language: English\n")
	case "versjon", "version":
		return versionLine() + "\n" + pluginMismatch(L)
	case "stats", "statistikk":
		return statusText(L, cfg) + statsText(L)
	case "status":
		return statusText(L, cfg)
	case "config", "konfig", "innstillinger", "settings":
		if rest != "" {
			return configSet(L, rest)
		}
		data, err := os.ReadFile(config.Path())
		if err != nil {
			return config.Path() + t(L, ": finnes ikke ennå (standardverdier brukes)\n", ": does not exist yet (defaults are used)\n")
		}
		return config.Path() + "\n" + strings.TrimRight(string(data), "\n") + "\n"
	case "husk", "remember":
		return remember(L, cfg, rest)
	case "glem", "forget":
		if rest == "alt" || rest == "all" {
			cfg.Notes = nil
		} else if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= len(cfg.Notes) {
			cfg.Notes = append(cfg.Notes[:n-1], cfg.Notes[n:]...)
		} else {
			var b strings.Builder
			for i, n := range cfg.Notes {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, n)
			}
			return b.String()
		}
		_ = config.Save(cfg)
		return t(L, "Glemt.\n", "Forgotten.\n")
	case "siste", "last", "logg", "log":
		n := 5
		if v, err := strconv.Atoi(rest); err == nil && v > 0 {
			n = v
		}
		return lastTurns(n)
	}
	return ""
}

func onOff(L msg.L, on bool) string {
	if on {
		return t(L, "på", "on")
	}
	return t(L, "av", "off")
}

func lastTurns(n int) string { return formatRows(store.Last(n, false)) }

func formatRows(rows []store.Row) string {
	var b strings.Builder
	for i := len(rows) - 1; i >= 0; i-- { // oldest first, like history
		r := rows[i]
		when := r.Started
		if tm, err := time.Parse("2006-01-02T15:04:05.000-07:00", r.Started); err == nil {
			when = tm.Format("02.01 15:04")
		}
		status := r.Outcome
		if r.Exit != nil {
			status += fmt.Sprintf(" %d", *r.Exit)
		}
		cmd := r.Script
		if cmd == "" {
			cmd = "–"
		}
		fmt.Fprintf(&b, "  %s  %-40s → %s  [%s, %s]\n", when, trunc(r.Request, 40), trunc(strings.ReplaceAll(cmd, "\n", "; "), 60), r.Risk, status)
	}
	return b.String()
}

// cmdLog: zshai log [n] [--changed] | zshai log --id <id>
func cmdLog(args []string) {
	n, changed, id := 20, false, ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--changed":
			changed = true
		case a == "--id" && i+1 < len(args):
			id = args[i+1]
			i++
		default:
			if v, err := strconv.Atoi(a); err == nil && v > 0 {
				n = v
			}
		}
	}
	if id == "" {
		fmt.Print(formatRows(store.Last(n, changed)))
		return
	}
	r, resp, eff, ok := store.Detail(id)
	if !ok {
		fmt.Fprintln(os.Stderr, "zshai log: no such id:", id)
		os.Exit(1)
	}
	fmt.Printf("%s  %s\n  cwd: %s\n  request: %s\n  command: %s\n  risk: %s  mode: %s  outcome: %s", r.UID, r.Started, r.Cwd, r.Request, r.Script, r.Risk, r.Mode, r.Outcome)
	if r.Exit != nil {
		fmt.Printf("  exit: %d", *r.Exit)
	}
	fmt.Printf("  model: %.2f s\n", r.ModelSeconds)
	for i, x := range resp {
		fmt.Printf("\n--- model call %d (%.2f s)%s\n", i+1, x.Seconds, map[bool]string{true: " error: " + x.Error}[x.Error != ""])
		if i > 0 {
			fmt.Printf("> %s\n", strings.ReplaceAll(x.Prompt, "\n", "\n> "))
		}
		fmt.Println(x.Response)
	}
	if len(eff) > 0 {
		fmt.Println("\n--- effects")
		for _, e := range eff {
			fmt.Printf("  %-8s %s", e.Action, e.Path)
			if e.Dest != "" {
				fmt.Printf(" → %s", e.Dest)
			}
			fmt.Printf("  (%s)\n", e.Via)
		}
	}
}

func sizeOf(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// themeArg accepts a theme name or its number in the list ("" if neither).
func themeArg(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	names := theme.Names()
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(names) {
		return names[n-1]
	}
	if slices.Contains(names, s) {
		return s
	}
	return ""
}

// themeList shows every theme as it looks, numbered, with the current one marked.
func themeList(L msg.L, cfg config.Config) string {
	no := L.No
	cur := cfg.Theme
	if cur == "" {
		cur = theme.Auto
	}
	var b strings.Builder
	b.WriteString(t(L, "Fargetemaer for zshai (ledeteksten din endres ikke):\n", "zshai colour themes (your prompt is not changed):\n"))
	mark := func(name string) string {
		if name == cur {
			return t(L, "  ← i bruk", "  ← current")
		}
		return ""
	}
	fmt.Fprintf(&b, "\n  1. auto  %s%s\n", t(L, "gold på mørk bakgrunn, light på lys", "gold on a dark background, light on a light one"), mark(theme.Auto))
	for i, th := range theme.All() {
		about := th.About[1]
		if no {
			about = th.About[0]
		}
		fmt.Fprintf(&b, "\n  %d. %s  %s%s\n", i+2, th.Name, about, mark(th.Name))
		b.WriteString(theme.Preview(th, no))
	}
	b.WriteString(t(L, "\nVelg med «zshai: tema <navn eller nummer>».\n", "\nPick one with \"zshai: theme <name or number>\".\n"))
	return b.String()
}

// cmdThemes is "zshai: tema" for the plugin: the list plus a one-key question.
func cmdThemes() {
	cfg := config.Load()
	L := msg.New(cfg.Language)
	list := themeList(L, cfg)
	list = list[:strings.LastIndex(strings.TrimRight(list, "\n"), "\n")+1] // drop the "pick with" line
	emit(map[string]string{"list": list, "count": strconv.Itoa(len(theme.Names())),
		"question": t(L, "Velg tema (1–"+strconv.Itoa(len(theme.Names()))+", annen tast = behold)", "Pick a theme (1–"+strconv.Itoa(len(theme.Names()))+", any other key = keep)")})
}

// langArg accepts the language in either language: "engelsk", "English", "en", "norsk", "bokmål", …
func langArg(s string) string {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(s), ".!")) {
	case "en", "eng", "engelsk", "english", "inglês", "englisch":
		return "en"
	case "no", "nb", "nob", "nor", "norsk", "bokmål", "bokmal", "norwegian", "norsk bokmål", "norwegian bokmål":
		return "no"
	}
	return ""
}

func remember(L msg.L, cfg config.Config, rest string) string {
	if rest == "" {
		return "zshai: husk <tekst>\n"
	}
	if len(rest) > 400 {
		rest = rest[:400]
	}
	cfg.Notes = append(cfg.Notes, rest)
	_ = config.Save(cfg)
	return t(L, "Husker: ", "Remembering: ") + rest + "\n"
}

// versionLine: "zshai 0.2.0 (a1b2c3d, built 2026-11-14) darwin/arm64"
func versionLine() string {
	return fmt.Sprintf("zshai %s (%s, built %s) %s/%s%s", version, commit, buildDate, runtime.GOOS, runtime.GOARCH, machineNote())
}

// machineNote tells when the binary doesn't match the machine: an amd64 build under
// Rosetta on an Apple Silicon Mac, or emulation on Linux.
func machineNote() string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		if out, err := exec.Command("sysctl", "-n", "sysctl.proc_translated").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
			return ", under Rosetta on arm64 (use zshai-darwin-arm64)"
		}
	}
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		m := strings.TrimSpace(string(out))
		want := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
		if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
			want = "aarch64"
		}
		if want != "" && m != want {
			return ", machine " + m
		}
	}
	return ""
}

// pluginMismatch warns when the plugin and the helper come from different builds
// (they are copied separately to a Mac).
func pluginMismatch(L msg.L) string {
	pv := os.Getenv("ZSHAI_PLUGIN_VERSION")
	if pv == "" || pv == version || pv == "dev" || version == "dev" {
		return ""
	}
	return t(L, "  Obs: pluginen er versjon "+pv+", hjelpeprogrammet "+version+". Kopier begge fra samme bygg.\n",
		"  Note: the plugin is version "+pv+", the helper "+version+". Copy both from the same build.\n")
}

func statusText(L msg.L, cfg config.Config) string {
	var b strings.Builder
	b.WriteString(versionLine() + "\n")
	if pv := os.Getenv("ZSHAI_PLUGIN_VERSION"); pv != "" {
		fmt.Fprintf(&b, "  plugin: %s\n", pv)
	}
	b.WriteString(pluginMismatch(L))
	host, _ := os.Hostname()
	if localmodel.IsLocal(cfg.Server) {
		fmt.Fprintf(&b, "  host: %s\n  %s\n  language: %s  theme: %s  auto: %s\n", host, localmodel.Status(), cfg.Language, cfg.Theme, onOff(L, cfg.AutoDetect))
	} else {
		fmt.Fprintf(&b, "  host: %s\n  model: %s @ %s\n  language: %s  theme: %s  auto: %s\n", host, cfg.Model, cfg.Server, cfg.Language, cfg.Theme, onOff(L, cfg.AutoDetect))
	}
	if c, err := azcat.Get(); err == nil {
		fmt.Fprintf(&b, "  az catalog: az %s, %d commands (%s)\n", c.Version, len(c.Commands), c.Source)
	}
	if e := cfg.Escalate; e.Server != "" && e.Model != "" {
		fmt.Fprintf(&b, t(L, "  større modell for siste forsøk: %s @ %s\n", "  larger model for the last try: %s @ %s\n"), e.Model, e.Server)
	}
	switch k := sandbox.Kind(); {
	case !cfg.TestRun:
		fmt.Fprint(&b, t(L, "  testkjøring: av (testRun)\n", "  test runs: off (testRun)\n"))
	case k != "":
		fmt.Fprintf(&b, t(L, "  testkjøring: i en kopi av mappa (%s)\n", "  test runs: in a copy of the folder (%s)\n"), k)
	case runtime.GOOS == "linux":
		fmt.Fprint(&b, t(L, "  testkjøring: ingen sandkasse (installer bubblewrap: sudo apt install bubblewrap)\n",
			"  test runs: no sandbox (install bubblewrap: sudo apt install bubblewrap)\n"))
	default:
		fmt.Fprint(&b, t(L, "  testkjøring: ingen sandkasse\n", "  test runs: no sandbox\n"))
	}
	fmt.Fprintf(&b, "  config: %s\n  log: %s\n", config.Path(), store.Path())
	return b.String()
}

func statsText(L msg.L) string {
	st, ok := store.Stats()
	if !ok || st.Turns == 0 {
		return t(L, "  Ingen forespørsler i loggen ennå.\n", "  No requests in the log yet.\n")
	}
	var b strings.Builder
	b.WriteString(t(L, "\nStatistikk\n", "\nStatistics\n"))
	fmt.Fprintf(&b, t(L, "  forespørsler: %d (siden %s)\n", "  requests: %d (since %s)\n"), st.Turns, st.Since)
	fmt.Fprintf(&b, t(L, "  kjørt: %d  feilet: %d  avbrutt: %d  endret: %d  vist: %d  spurt tilbake: %d\n",
		"  ran: %d  failed: %d  cancelled: %d  edited: %d  shown: %d  asked back: %d\n"), st.Ran, st.Failed, st.Cancelled, st.Edited, st.Shown, st.Asked)
	fmt.Fprintf(&b, t(L, "  rettet etter feil: %d  avvist av kontrollen: %d  angret: %d\n", "  fixed after failure: %d  refused by checks: %d  undone: %d\n"), st.Fixes, st.Refused, st.Undone)
	fmt.Fprintf(&b, t(L, "  modelltid: %.2f s i snitt, %.2f s median\n", "  model time: %.2f s average, %.2f s median\n"), st.AvgModel, st.MedianModel)
	if len(st.TopTools) > 0 {
		fmt.Fprintf(&b, t(L, "  mest brukt: %s\n", "  most used: %s\n"), strings.Join(st.TopTools, ", "))
	}
	return b.String()
}

// promptMeta: zshai: prompt [show|vis|reset|tilbakestill]; plain "zshai: prompt" edits it.
func promptMeta(L msg.L, cfg config.Config, rest string) string {
	switch strings.ToLower(rest) {
	case "", "edit", "rediger", "endre":
		p, err := prompt.Ensure()
		if err != nil {
			return err.Error() + "\n"
		}
		return editMarker + p
	case "show", "vis":
		tmpl, custom := prompt.Template()
		ci := contextinfo.Build(contextinfo.Input{Language: cfg.Language})
		model := cfg.Model
		if localmodel.IsLocal(cfg.Server) {
			model = localmodel.Model
		}
		var b strings.Builder
		if custom {
			fmt.Fprintf(&b, t(L, "# fra %s\n", "# from %s\n"), prompt.Path())
		} else {
			b.WriteString(t(L, "# standard (zshai: prompt lager en fil du kan endre)\n", "# default (zshai: prompt creates a file you can edit)\n"))
		}
		for _, p := range prompt.Problems(tmpl) {
			fmt.Fprintf(&b, "# %s: %s\n", t(L, "advarsel", "warning"), p)
		}
		b.WriteString(prompt.Render(tmpl, llm.SystemFor(model), prompt.MachineValues(ci.OSName, ci.Flavour, ci.Manager, cfg.Language)) + "\n")
		return b.String()
	case "reset", "tilbakestill", "standard", "default":
		p, err := prompt.Reset()
		if err != nil {
			return err.Error() + "\n"
		}
		return fmt.Sprintf(t(L, "  Systemprompten er satt tilbake til standard (%s).\n", "  The system prompt is back to the default (%s).\n"), p)
	}
	return t(L, "  zshai: prompt (rediger) · zshai: prompt vis · zshai: prompt tilbakestill\n",
		"  zshai: prompt (edit) · zshai: prompt show · zshai: prompt reset\n")
}
