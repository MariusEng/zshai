// zshai: natural language for zsh (see PLAN.md). This binary is the helper the
// oh-my-zsh plugin calls; it can also be used directly for testing:
//
//	zshai check -- 'rm *.log'          risk, validation and dry run for a command
//	zshai ask -- 'how many lines in x' one turn, printed as zsh assignments
//	zshai route -- 'find all pdfs'     exit 0 = AI, 1 = shell
//	zshai undo                         reverse the last AI move/delete/git reset
//	zshai --lang no|en
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/azcat"
	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/contextinfo"
	"github.com/MariusEng/zshai/internal/dryrun"
	"github.com/MariusEng/zshai/internal/journal"
	"github.com/MariusEng/zshai/internal/llm"
	"github.com/MariusEng/zshai/internal/localmodel"
	"github.com/MariusEng/zshai/internal/msg"
	"github.com/MariusEng/zshai/internal/prompt"
	"github.com/MariusEng/zshai/internal/risk"
	"github.com/MariusEng/zshai/internal/router"
	"github.com/MariusEng/zshai/internal/sandbox"
	"github.com/MariusEng/zshai/internal/shell"
	"github.com/MariusEng/zshai/internal/spinner"
	"github.com/MariusEng/zshai/internal/store"
	"github.com/MariusEng/zshai/internal/theme"
	"github.com/MariusEng/zshai/internal/tools"
	"github.com/MariusEng/zshai/internal/validate"
)

// Set at build time by the Makefile (-X): VERSION file, git commit, build date (UTC).
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "route":
		os.Exit(cmdRoute(os.Args[2:]))
	case "typo":
		// for the plugin: the command a mistyped first word was meant to be
		os.Exit(cmdTypo(os.Args[2:]))
	case "ask":
		cmdAsk(os.Args[2:])
	case "confirm":
		cmdConfirm(os.Args[2:])
	case "done":
		cmdDone(os.Args[2:])
	case "outcome":
		// the plugin reports what the user did with a question: cancelled | edited
		if len(os.Args) == 4 && (os.Args[3] == "cancelled" || os.Args[3] == "edited") {
			store.SetOutcome(os.Args[2], os.Args[3])
		}
	case "log":
		cmdLog(os.Args[2:])
	case "show":
		cmdShow(os.Args[2:])
	case "debug":
		// the same as "zshai: debug [n|id]"
		cfg := config.Load()
		fmt.Print(debugMeta(msg.New(cfg.Language), cfg, strings.Join(os.Args[2:], " ")))
	case "undo", "angre":
		fmt.Println(undo(msg.New(config.Load().Language)))
	case "meta":
		// exit 3: not one of zshai's own commands, so the plugin sends it to the model
		out, ok := meta(strings.Join(argsAfterDash(os.Args[2:]), " "))
		if !ok {
			os.Exit(3)
		}
		if path, edit := strings.CutPrefix(out, editMarker); edit {
			// exit 4: the plugin opens this file in $EDITOR
			fmt.Println(path)
			os.Exit(4)
		}
		fmt.Print(out)
	case "colors", "colours":
		// for the plugin: the theme's colour codes. --bg dark|light from the terminal.
		bg := ""
		if len(os.Args) > 3 && os.Args[2] == "--bg" {
			bg = os.Args[3]
		}
		name := config.Load().Theme
		_, fixed := theme.Get(name)
		fmt.Print(theme.Zsh(theme.Resolve(name, bg), !fixed))
	case "themes":
		cmdThemes()
	case "settings":
		// for the plugin: the settings it needs, as zsh assignments
		c := config.Load()
		fmt.Printf("_zshai_auto=%d _zshai_fix=%d _zshai_typos=%d\n", b2i(c.AutoDetect), b2i(c.FixAttempts > 0), b2i(c.Typos))
	case "auto":
		// for the plugin: is auto-detect on?
		if config.Load().AutoDetect {
			fmt.Println(1)
		} else {
			fmt.Println(0)
		}
	case "fmt", "format":
		// PowerShell-style tables and lists for JSON / CSV (Format-Table, Format-List)
		os.Exit(cmdFmt(os.Args[2:]))
	case "check":
		cmdCheck(os.Args[2:])
	case "model":
		// the bundled model (server "local"): status | start | stop
		sub := "status"
		if len(os.Args) > 2 {
			sub = os.Args[2]
		}
		switch sub {
		case "start":
			if err := localmodel.Ensure(30 * time.Second); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case "stop":
			if !localmodel.Stop() {
				fmt.Println("llama-server was not started by zshai")
			}
			return
		case "list", "ls":
			fmt.Print(localmodel.List())
			return
		case "install", "add", "use", "remove", "rm":
			if len(os.Args) < 4 {
				fmt.Fprintf(os.Stderr, "zshai model %s <1.5b|3b>\n", sub)
				os.Exit(2)
			}
			name := strings.ToLower(os.Args[3])
			if sub == "remove" || sub == "rm" {
				if err := localmodel.Remove(name); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				fmt.Println("removed", name)
				return
			}
			if sub == "install" || sub == "add" {
				if err := localmodel.Install(name, os.Stderr); err != nil {
					fmt.Fprintln(os.Stderr, "zshai model install:", err)
					os.Exit(1)
				}
			}
			if _, ok := localmodel.Installed()[name]; !ok {
				fmt.Fprintf(os.Stderr, "the %s model is not installed (zshai model install %s)\n", name, name)
				os.Exit(1)
			}
			c := config.Load()
			c.LocalModel = name
			if err := config.Save(c); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			localmodel.Stop() // the next request starts the chosen model
			fmt.Println("using", name)
			fmt.Print(localmodel.List())
			return
		}
		fmt.Println(localmodel.Status())
	case "az-catalog":
		cmdAzCatalog()
	case "--lang", "lang":
		if len(os.Args) < 3 || (os.Args[2] != "en" && os.Args[2] != "no") {
			fmt.Fprintln(os.Stderr, "zshai --lang en|no")
			os.Exit(2)
		}
		c := config.Load()
		c.Language = os.Args[2]
		if err := config.Save(c); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("language:", c.Language)
	case "--version", "version":
		fmt.Println(versionLine())
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `zshai — natural language for zsh (helper for the oh-my-zsh plugin)

  zshai check -- <command>     risk, validation and dry run for a command
  zshai ask [--cwd D] -- <request>
  zshai route -- <line>        exit 0 = send to AI, 1 = run as shell
  zshai fmt [-t|-l] [file]     JSON/CSV as a PowerShell-style table or list (zshai fmt -h)
  zshai undo                   reverse the last AI move/delete/git reset
  zshai log [n] [--changed]    the latest requests from the log (~/.local/state/zshai/zshai.db)
  zshai log --id <id>          one request: prompts, model replies, effects
  zshai debug [n|id]           write a debug report of the last n requests to a file
  zshai meta -- 'zshai: help'  zshai's own commands (exit 3: not one, it's a request)
  zshai model [start|stop]     the bundled model (config "server": "local")
  zshai model list|install|use|remove <1.5b|3b>      choose zshai-mini's size
  zshai az-catalog             re-extract the Azure CLI catalog from your az
  zshai --lang en|no
`)
}

func argsAfterDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return args
}

// ---------------------------------------------------------------- shared state

type turnState struct {
	ID        string       `json:"id"`
	Request   string       `json:"request"`
	Cwd       string       `json:"cwd"`
	Command   string       `json:"command"`
	Level     string       `json:"level"`
	Plan      *dryrun.Plan `json:"plan,omitempty"`
	AzYes     bool         `json:"azYes"`
	Started   time.Time    `json:"started"`
	Model     string       `json:"model"`
	ModelMs   int64        `json:"modelMs"`
	Install   string       `json:"install,omitempty"`
	Asked     string       `json:"asked,omitempty"`
	GitBackup bool         `json:"gitBackup"`
	FixOf     string       `json:"fixOf,omitempty"`
	FixDepth  int          `json:"fixDepth,omitempty"`
}

func turnPath(id string) string { return filepath.Join(config.CacheDir(), "turns", id+".json") }

func saveTurn(t *turnState) {
	_ = os.MkdirAll(filepath.Dir(turnPath(t.ID)), 0o700)
	b, _ := json.Marshal(t)
	_ = os.WriteFile(turnPath(t.ID), b, 0o600)
	// keep the cache small: drop turns older than a day
	if ents, err := os.ReadDir(filepath.Dir(turnPath(t.ID))); err == nil && len(ents) > 200 {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				os.Remove(filepath.Join(filepath.Dir(turnPath(t.ID)), e.Name()))
			}
		}
	}
}

func loadTurn(id string) (*turnState, error) {
	if strings.ContainsAny(id, "/.") {
		return nil, errors.New("bad id")
	}
	b, err := os.ReadFile(turnPath(id))
	if err != nil {
		return nil, err
	}
	var t turnState
	return &t, json.Unmarshal(b, &t)
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// emit prints zsh assignments for the plugin: zshai_r=( key 'value' ... ).
func emit(kv map[string]string) {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("zshai_r=(")
	for _, k := range keys {
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString(" ")
		sb.WriteString(zq(kv[k]))
	}
	sb.WriteString(" )\n")
	fmt.Print(sb.String())
}

func zq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// loadAliases reads the plugin's `alias -L` dump.
func loadAliases() shell.Aliases {
	al := shell.Aliases{}
	b, err := os.ReadFile(filepath.Join(config.CacheDir(), "aliases.zsh"))
	if err != nil {
		return al
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "alias ")
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, "-- ")
		rest = strings.TrimPrefix(rest, "-g ")
		name, val, ok := strings.Cut(rest, "=")
		if !ok {
			continue
		}
		if s, err := shell.Parse("x "+val, nil); err == nil && len(s.Cmds) > 0 && len(s.Cmds[0].Args) > 0 && s.Cmds[0].Lit[0] {
			val = s.Cmds[0].Args[0]
		} else {
			val = strings.Trim(val, "'")
		}
		al[name] = val
	}
	return al
}

func loadFunctions() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(config.CacheDir(), "functions.txt"))
	if err != nil {
		return m
	}
	for _, f := range strings.Fields(string(b)) {
		m[f] = true
	}
	return m
}

func azEnv(ci *contextinfo.Info) (*azcat.Catalog, azcat.Defaults, string) {
	cat, _ := azcat.Get()
	var d azcat.Defaults
	sub := ""
	if ci != nil && ci.Azure != nil {
		d = azcat.Defaults{Group: ci.Azure.DefGroup, Location: ci.Azure.DefLocation}
		sub = ci.Azure.Subscription
	}
	return cat, d, sub
}

// ---------------------------------------------------------------- route

func cmdRoute(args []string) int {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	cwd := fs.String("cwd", "", "")
	_ = fs.Parse(args)
	line := strings.Join(fs.Args(), " ")
	if *cwd != "" {
		_ = os.Chdir(*cwd)
	}
	d := router.Decide(line, router.Env{Cwd: *cwd})
	if d.AI {
		if d.Hint {
			fmt.Println("hint")
		}
		return 0
	}
	return 1
}

// ---------------------------------------------------------------- ask

func cmdAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	cwd := fs.String("cwd", "", "")
	last := fs.String("last", "", "")
	lastExit := fs.Int("exit", 0, "")
	// fix-after-failure (PLAN.md §2): the plugin reports a zshai command that failed
	fixOf := fs.String("fix-of", "", "")
	failExit := fs.Int("fail-exit", 0, "")
	stderrFile := fs.String("stderr", "", "")
	newReq := fs.Bool("new", false, "a new request (zshai: …), never the answer to an earlier question")
	_ = fs.Parse(args)
	request := strings.TrimSpace(strings.Join(fs.Args(), " "))
	cfg := config.Load()
	L := msg.New(cfg.Language)
	var failed *turnState
	var errText string
	if *fixOf != "" {
		failed, errText = fixInput(*fixOf, *failExit, *stderrFile, cfg)
		if failed == nil {
			emit(map[string]string{"status": "none"})
			return
		}
		request = failed.Request
		store.AddFix(failed.ID)
	}
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	if err := os.Chdir(*cwd); err != nil {
		emit(map[string]string{"status": "error", "msg": L.CwdGone(*cwd)})
		return
	}
	label := L.Thinking()
	if failed != nil {
		label = L.Fixing()
	}
	spin := spinner.Start(label)
	defer spin.Stop()
	t0 := time.Now()
	id := newID()
	turn := journal.Turn{ID: id, Start: t0, Cwd: *cwd, Request: request, Model: cfg.Model, Lang: cfg.Language, Mode: "run"}
	if failed != nil {
		turn.Mode, turn.FixOf = "fix", failed.ID
	}
	defer func() {
		turn.TotalMs = time.Since(t0).Milliseconds()
		journal.LogTurn(turn)
	}()

	// a follow-up to a clarifying question
	prev := loadPendingAsk()
	fullReq := request
	answered := "" // the user's answer to zshai's question, when this is one
	if prev != nil && failed == nil && !*newReq && time.Since(prev.Started) < 10*time.Minute {
		fullReq = fmt.Sprintf("%s (earlier request: %q; you asked: %q; this is the answer)", request, prev.Request, prev.Asked)
		answered = request
	}
	clearPendingAsk()

	ci := contextinfo.Build(contextinfo.Input{Cwd: *cwd, Request: request, Last: *last, LastExit: *lastExit, Language: cfg.Language, Notes: cfg.Notes})
	cat, azDef, sub := azEnv(ci)
	aliases := loadAliases()
	venv := validate.Env{OS: ci.OS, Cwd: *cwd, Aliases: aliases, Functions: loadFunctions(), Az: cat, AzDef: azDef}

	client := llm.Client{Server: cfg.Server, Model: cfg.Model, APIKey: cfg.APIKey, Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}
	if localmodel.IsLocal(cfg.Server) {
		// the bundled model: start llama-server on first use
		if !localmodel.Healthy() {
			spin.Set(L.StartingModel())
		}
		if err := localmodel.Ensure(20 * time.Second); err != nil {
			turn.Outcome, turn.Error = "error", err.Error()
			emit(map[string]string{"status": "error", "msg": L.Unreachable(err)})
			return
		}
		client.Server, client.Model = localmodel.URL(), localmodel.Model
		turn.Model = localmodel.Model
		spin.Set(label)
	}
	// the system prompt: the user's template (zshai: prompt) around zshai's built-in rules
	tmpl, _ := prompt.Template()
	client.System = prompt.Render(tmpl, llm.SystemFor(client.Model), prompt.MachineValues(ci.OSName, ci.Flavour, ci.Manager, cfg.Language))
	journal.SetSystemPrompt(client.System)
	userMsg := ci.Render(fullReq)
	if failed != nil {
		userMsg += fmt.Sprintf("\nThe command you gave for this request failed.\nfailed command: %s\nexit code: %d\nerror output:\n%s\n"+
			"Reply with a corrected command for the same request, in the same format. Don't repeat the failed command.\n",
			failed.Command, *failExit, errText)
		if *failExit == 0 {
			userMsg += "It exited 0, but the error output shows a part of it failed, so its result is wrong.\n"
		}
	}
	msgs := []llm.Message{{Role: "user", Content: userMsg}}

	var ans llm.Answer
	var script *shell.Script
	var vres validate.Result
	var problems []string
	var missing []string
	var tested *sandbox.Check // what a test run in a copy of the folder did
	show := llm.WantsShow(request) && failed == nil
	if show {
		msgs[0].Content += "mode: show\n"
	} else {
		msgs[0].Content += "mode: run\n"
	}
	escalated := ""
	for attempt := 0; attempt < 3; attempt++ {
		switch attempt {
		case 1:
			spin.Set(L.Retrying())
		case 2:
			spin.Set(L.ThinkingHarder())
		}
		var raw string
		var dur time.Duration
		var err error
		if attempt == 2 && cfg.Escalate.Server != "" && cfg.Escalate.Model != "" {
			// thinking harder: the last try goes to a larger model, with the full rules
			// and the conversation so far (the failed answers and what was wrong)
			esc := llm.Client{Server: cfg.Escalate.Server, Model: cfg.Escalate.Model, APIKey: cfg.Escalate.APIKey,
				Timeout: time.Duration(max(cfg.Escalate.TimeoutSeconds, cfg.TimeoutSeconds)) * time.Second}
			esc.System = prompt.Render(tmpl, llm.SystemFor(esc.Model), prompt.MachineValues(ci.OSName, ci.Flavour, ci.Manager, cfg.Language))
			at := time.Now()
			raw, dur, err = esc.Chat(context.Background(), msgs)
			turn.ModelMs += dur.Milliseconds()
			call := store.Response{Model: esc.Model, System: esc.System, At: at, Prompt: msgs[len(msgs)-1].Content, Response: raw, Seconds: dur.Seconds()}
			if err != nil {
				call.Error = err.Error()
			}
			turn.Calls = append(turn.Calls, call)
			if err == nil {
				escalated = esc.Model
			}
		}
		if escalated == "" {
			if attempt == 2 && err != nil {
				err = nil // the larger model is unreachable: the last try stays with the usual one
			}
			at := time.Now()
			raw, dur, err = client.Chat(context.Background(), msgs)
			turn.ModelMs += dur.Milliseconds()
			call := store.Response{At: at, Prompt: msgs[len(msgs)-1].Content, Response: raw, Seconds: dur.Seconds()}
			if err != nil {
				call.Error = err.Error()
			}
			turn.Calls = append(turn.Calls, call)
		}
		if err != nil {
			turn.Outcome, turn.Error = "error", err.Error()
			emit(map[string]string{"status": "error", "msg": L.Unreachable(err)})
			return
		}
		ans, err = llm.Parse(raw)
		turn.ModelRisk = ans.Risk
		if err != nil {
			problems = []string{err.Error()}
		} else if ans.Ask != "" && ans.Script == "" && answered != "" {
			// the user already answered a question: one question per request, never a loop
			// ("Skal alle … slettes?" → "i denne mappen" → the same question again, forever)
			problems = []string{fmt.Sprintf("the user already answered your question (%q is the answer to %q): "+
				"don't ask again; write the command for the earlier request, using that answer", answered, prev.Asked)}
		} else if ans.Ask != "" && ans.Script == "" {
			turn.Outcome, turn.Mode = "asked", "ask"
			savePendingAsk(&turnState{ID: id, Request: fullReq, Asked: ans.Ask, Started: time.Now()})
			emit(map[string]string{"status": "answer", "msg": ans.Ask, "followup": "1"})
			return
		} else if ans.Script == "" && len(ans.Needs) > 0 && installed(ans.Needs[0], cfg) {
			// small models sometimes ask for a program that is already there: send it back
			problems = []string{ans.Needs[0] + " is installed (it is in \"have:\"); write the command with it and leave n: empty"}
		} else if ans.Script == "" && len(ans.Needs) > 0 {
			emitNeeds(ans.Needs[0], request, *cwd, cfg, L, &turn, id)
			return
		} else if ans.Script == "" {
			problems = []string{"the command is empty"}
		} else {
			script, err = shell.Parse(ans.Script, aliases)
			if err == nil {
				// 7-Zip: the name installed here (7z → 7zz on a Mac), and -o glued to its folder
				if sz := validate.SevenZip(ans.Script); sz != "" {
					if s2, err2 := shell.Parse(sz, aliases); err2 == nil {
						ans.Script, script = sz, s2
					}
				}
			}
			if err == nil && cfg.Tables != "column" && !(show || ans.Show) {
				// PowerShell-style tables: the jq/column table recipes become zshai fmt -t.
				// Before the checks, so the command that runs is the one that gets checked
				// (zshai fmt on the real file, "this is the command that failed").
				if ps := validate.PSTable(ans.Script, *cwd); ps != "" {
					if s2, err2 := shell.Parse(ps, aliases); err2 == nil {
						ans.Script, script = ps, s2
					}
				}
			}
			if err != nil {
				problems = []string{"syntax error: " + err.Error()}
			} else {
				vres = validate.Check(script, venv)
				problems = vres.Problems
				missing = vres.Missing
				if !(show || ans.Show) {
					// a file of the kind asked for, in the folder asked for (not /tmp)
					home, _ := os.UserHomeDir()
					problems = append(problems, validate.NewFile(script, request, *cwd, home)...)
				}
				if show || ans.Show {
					// explaining a command for a program that isn't here is fine: no retry for that
					problems = slices.DeleteFunc(slices.Clone(problems), func(p string) bool {
						if name, ok := strings.CutPrefix(p, "command not found: "); ok {
							missing = append(missing, name)
							return true
						}
						return false
					})
				}
				if failed != nil && strings.TrimSpace(ans.Script) == strings.TrimSpace(failed.Command) {
					problems = append(problems, "this is the command that failed; change it so it works")
				}
				if attempt == 0 && len(problems) == 0 {
					// soft: asked once, accepted if the model keeps it
					problems = append(validate.Trimmed(script, request), validate.LsTotal(script)...)
					problems = append(problems, validate.YesNo(script)...)
					problems = append(problems, validate.SortAfterTable(script)...)
					problems = append(problems, validate.JqCompact(script, request)...)
					problems = append(problems, validate.CSVSortHeader(script)...)
					problems = append(problems, validate.TableAsked(script, request)...)
					problems = append(problems, validate.QuotedNames(script, request)...)
					if home, err := os.UserHomeDir(); err == nil {
						problems = append(problems, validate.RemotePath(script, home, *cwd)...)
					}
				}
				if len(vres.Missing) > 0 && len(problems) == 0 && !(show || ans.Show) {
					turn.Command = ans.Script
					emitNeeds(vres.Missing[0], request, *cwd, cfg, L, &turn, id)
					return
				}
				// a command that changes files: run it once in a sandboxed copy of the folder
				tested = nil
				if len(problems) == 0 && !(show || ans.Show) && cfg.TestRun {
					home, _ := os.UserHomeDir()
					rk := risk.Classify(script, risk.Env{Home: home, OS: ci.OS, Az: cat, ProtectedSubs: cfg.ProtectedSubscriptions, CurrentSub: sub})
					if (rk.Level == risk.Change || rk.Level == risk.Delete) && testable(script) {
						spin.Set(L.Testing())
						t0 := time.Now()
						res, err := sandbox.Run(ans.Script, *cwd, 3*time.Second)
						if err == nil {
							c := sandbox.Judge(res, request)
							if fo := validate.FilesOnly(ans.Script); c.DirsHit && fo != "" {
								// "rm zshai*" hit folders the user didn't ask about: keep plain
								// files (zsh's (.)) and test that instead, without the model
								if s2, err2 := shell.Parse(fo, aliases); err2 == nil {
									if res2, err3 := sandbox.Run(fo, *cwd, 3*time.Second); err3 == nil {
										c2 := sandbox.Judge(res2, request)
										turn.Tests = append(turn.Tests, testLog(ans.Script, res, c, L.Tested(c), time.Since(t0)))
										res.Close()
										ans.Script, script, res, c = fo, s2, res2, c2
									}
								}
							}
							res.Close()
							problems = append(problems, c.Problems...)
							tested = &c
							turn.Tests = append(turn.Tests, testLog(ans.Script, res, c, L.Tested(c), time.Since(t0)))
						} else {
							turn.Tests = append(turn.Tests, store.Test{Command: ans.Script, Exit: -1, Stderr: "not test-run: " + err.Error()})
						}
					}
				}
			}
		}
		if len(problems) == 0 {
			break
		}
		turn.Problems = append(turn.Problems, problems...)
		if attempt == 2 {
			break
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: raw},
			llm.Message{Role: "user", Content: "That reply failed zshai's checks:\n- " + strings.Join(problems, "\n- ") +
				"\nFix it and reply again in exactly the same format (r:/a:/n:/x:/---/command)."})
	}
	// the model's own "m: show" only counts with an explanation: "vis de 5 største filene"
	// means show me the files, and a show answer without e: lines is the model misreading "vis"
	// show mode is decided by the request alone (llm.WantsShow): the model's own "m: show"
	// turned "vis meg config", "how long has this machine been running" etc. into shows
	if show && ans.Script != "" {
		// learn mode: print the command and what it does, never run it
		out := map[string]string{"status": "show", "id": id, "cmd": ans.Script, "explain": strings.Join(ans.Explain, "\n")}
		var notes []string
		for _, m := range missing {
			inst := ""
			if t, ok := tools.Lookup(m); ok {
				inst = tools.InstallCommand(t, tools.Manager(cfg.PackageManager))
			}
			notes = append(notes, L.NeedsShow(m, inst))
		}
		if len(problems) > 0 {
			notes = append(notes, L.Unchecked(problems))
		}
		if script != nil {
			home, _ := os.UserHomeDir()
			rk := risk.Classify(script, risk.Env{Home: home, OS: ci.OS, Az: cat, ProtectedSubs: cfg.ProtectedSubscriptions, CurrentSub: sub})
			out["level"], out["label"] = rk.Level.String(), L.Label(rk.Level)
			turn.Risk = rk.Level.String()
			if rk.Level < risk.Danger {
				out["hint"] = L.ShowHint() // the plugin puts the command in history for ↑
			}
		}
		if len(notes) > 0 {
			out["note"] = strings.Join(notes, "; ")
		}
		turn.Command, turn.Outcome, turn.Mode = ans.Script, "shown", "show"
		emit(out)
		return
	}
	if len(problems) > 0 {
		turn.Outcome = "refused"
		if answered != "" && ans.Script == "" && ans.Ask != "" {
			// it still asked: say so plainly instead of the checks' English
			emit(map[string]string{"status": "error", "msg": L.AskedAgain()})
			return
		}
		emit(map[string]string{"status": "error", "msg": L.Gave(problems), "cmd": ans.Script})
		return
	}

	home, _ := os.UserHomeDir()
	rk := risk.Classify(script, risk.Env{Home: home, OS: ci.OS, Az: cat, ProtectedSubs: cfg.ProtectedSubscriptions, CurrentSub: sub})
	level := rk.Level
	plan := dryrun.Build(script, rk, dryrun.Env{Cwd: *cwd, Home: home, Az: cat})
	if plan != nil && plan.Raise && level < risk.Delete {
		level = risk.Delete
	}
	ts := &turnState{ID: id, Request: request, Cwd: *cwd, Command: ans.Script, Level: level.String(), Plan: plan, Started: t0, Model: cfg.Model, ModelMs: turn.ModelMs}
	if failed != nil {
		ts.FixOf, ts.FixDepth = failed.ID, failed.FixDepth+1
	}
	for _, c := range script.Cmds {
		if c.Name == "az" && len(script.Cmds) == 1 && level >= risk.Delete {
			if _, cmd, _ := cat.Resolve(c.Args); cmd != nil && cmd.Yes && !contains(c.Args, "--yes") && !contains(c.Args, "-y") {
				ts.AzYes = true
			}
		}
		if c.Name == "git" && level >= risk.Delete {
			ts.GitBackup = true
		}
	}
	saveTurn(ts)
	turn.Command, turn.Risk = ans.Script, level.String()

	out := map[string]string{"id": id, "cmd": ans.Script, "level": level.String(), "label": L.Label(level), "cancel": L.Cancelled()}
	var notes []string
	notes = append(notes, vres.Warnings...)
	if len(rk.Remote) > 0 {
		notes = append(notes, L.Remote(rk.Remote))
	}
	if tested != nil {
		out["tested"] = L.Tested(*tested)
	}
	if escalated != "" {
		notes = append(notes, L.Escalated(escalated))
	}
	// the dry-run question explains the risk in the user's language; otherwise show the rule's reason
	if plan == nil || plan.Unknown {
		for _, f := range rk.Findings {
			if f.Level == risk.Danger {
				notes = append(notes, f.Why)
			}
		}
	}
	if len(notes) > 0 {
		out["note"] = strings.Join(dedupe(notes), "; ")
	}
	if failed != nil {
		out["failed"] = L.Failed(*failExit)
	}
	if level == risk.Read && failed == nil {
		out["status"] = "run"
		turn.Outcome = "ran"
	} else {
		out["status"] = "confirm"
		out["keys"] = L.Keys(level)
		q := L.Question(plan, level, *cwd, sub)
		if q == "" {
			q = L.Run()
			if failed != nil {
				q = L.RunFixed()
			}
		}
		out["question"] = q
		turn.Outcome = "asked-confirm"
	}
	emit(out)
}

// testLog: a test run for the log (zshai log --id, zshai: debug).
func testLog(cmd string, r *sandbox.Result, c sandbox.Check, summary string, d time.Duration) store.Test {
	t := store.Test{Command: cmd, Exit: r.Exit, TimedOut: r.TimedOut, Denied: r.Denied, Stdout: r.Stdout, Stderr: r.Stderr,
		Problems: c.Problems, Summary: summary, Seconds: d.Seconds()}
	if len(t.Stdout) > 4000 {
		t.Stdout = t.Stdout[:4000] + "\n…"
	}
	for _, e := range r.Effects {
		s := e.Kind + " " + e.Path
		if e.From != "" {
			s = e.Kind + " " + e.From + " → " + e.Path
		}
		if !e.IsDir && e.Kind != "deleted" {
			s += fmt.Sprintf(" (%d B)", e.Size)
		}
		t.Effects = append(t.Effects, s)
	}
	return t
}

// testable: nothing in the command acts outside the folder's files (system settings,
// processes, other machines, package managers) or waits for a person.
func testable(s *shell.Script) bool {
	if s.HasFunc {
		return false
	}
	for _, c := range s.Cmds {
		if sandbox.Untestable[c.Name] || c.Remote != "" || slices.Contains(c.Wrappers, "sudo") || slices.Contains(c.Wrappers, "doas") {
			return false
		}
		if c.Name == "git" && len(c.Args) > 0 && slices.Contains([]string{"push", "pull", "fetch", "clone", "remote", "submodule", "lfs"}, c.Args[0]) {
			return false
		}
	}
	return true
}

func emitNeeds(tool, request, cwd string, cfg config.Config, L msg.L, turn *journal.Turn, id string) {
	t, known := tools.Lookup(tool)
	mgr := tools.Manager(cfg.PackageManager)
	turn.Outcome, turn.Mode = "install", "install"
	if !known {
		emit(map[string]string{"status": "answer", "msg": L.Needs(tool, "", "")})
		return
	}
	if _, err := exec.LookPath(t.BinFor(mgr)); err == nil {
		emit(map[string]string{"status": "error", "msg": "model asked for " + tool + ", which is installed; try again"})
		return
	}
	inst := tools.InstallCommand(t, mgr)
	if inst == "" || cfg.SuggestInstall == "never" {
		emit(map[string]string{"status": "answer", "msg": L.Needs(tool, "", "")})
		return
	}
	if cfg.SuggestInstall == "mention" {
		emit(map[string]string{"status": "answer", "msg": L.Needs(tool, inst, "")})
		return
	}
	ts := &turnState{ID: id, Request: request, Cwd: cwd, Command: inst, Level: risk.Install.String(), Install: tool, Started: time.Now()}
	saveTurn(ts)
	emit(map[string]string{"status": "install", "id": id, "cmd": inst, "level": "install", "label": L.Label(risk.Install),
		"question": L.Needs(tool, inst, ""), "keys": L.Keys(risk.Install), "request": request, "cancel": L.Cancelled()})
}

// installed: the program (or its package's binary for this package manager) is on PATH.
func installed(tool string, cfg config.Config) bool {
	if _, err := exec.LookPath(tool); err == nil {
		return true
	}
	if t, ok := tools.Lookup(tool); ok {
		if _, err := exec.LookPath(t.BinFor(tools.Manager(cfg.PackageManager))); err == nil {
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func pendingAskPath() string { return filepath.Join(config.CacheDir(), "pending-ask.json") }

func savePendingAsk(t *turnState) {
	_ = os.MkdirAll(config.CacheDir(), 0o700)
	b, _ := json.Marshal(t)
	_ = os.WriteFile(pendingAskPath(), b, 0o600)
}

func loadPendingAsk() *turnState {
	b, err := os.ReadFile(pendingAskPath())
	if err != nil {
		return nil
	}
	var t turnState
	if json.Unmarshal(b, &t) != nil {
		return nil
	}
	return &t
}

func clearPendingAsk() { os.Remove(pendingAskPath()) }

// ---------------------------------------------------------------- confirm / done

func cmdConfirm(args []string) {
	if len(args) < 1 {
		emit(map[string]string{"status": "error", "msg": "confirm: missing id"})
		return
	}
	cfg := config.Load()
	L := msg.New(cfg.Language)
	t, err := loadTurn(args[0])
	if err != nil {
		emit(map[string]string{"status": "error", "msg": "confirm: " + err.Error()})
		return
	}
	cmd := t.Command
	// git: save a backup before anything that can lose work
	store.SetOutcome(t.ID, "confirmed")
	if t.GitBackup {
		if e, ok := gitBackup(t); ok {
			_ = journal.Append(e)
			store.AddEffects(t.ID, []store.Effect{{Action: "backup", Path: e.Repo, Dest: e.Ref, Via: "git"}})
		}
	}
	// deletes zshai can do itself go to the trash
	if p := t.Plan; p != nil && p.Trashable && !p.Unknown && len(p.Items) > 0 {
		var paths []string
		for _, it := range p.Items {
			paths = append(paths, absPath(it.Path, t.Cwd))
		}
		moved, err := trashPut(paths)
		if len(moved) > 0 {
			_ = journal.Append(journal.Entry{ID: t.ID, Time: time.Now(), Kind: "trash", Request: t.Request, Command: t.Command, Moves: moved, Repo: p.Repo})
			var eff []store.Effect
			for _, m := range moved {
				eff = append(eff, store.Effect{Action: "deleted", Path: m.From, Dest: m.To, Size: sizeOf(m.To), Via: "trash"})
			}
			store.AddEffects(t.ID, eff)
			store.SetOutcome(t.ID, "trashed")
		}
		if err != nil {
			emit(map[string]string{"status": "error", "msg": err.Error()})
			return
		}
		emit(map[string]string{"status": "done", "msg": L.Trashed(p.Files, p.Bytes)})
		return
	}
	if p := t.Plan; p != nil && p.Kind == "mv" && !p.Unknown {
		var moves []trashMove
		for _, it := range p.Items {
			moves = append(moves, trashMove{From: absPath(it.Path, t.Cwd), To: absPath(it.Dest, t.Cwd)})
		}
		_ = journal.Append(journal.Entry{ID: t.ID, Time: time.Now(), Kind: "move", Request: t.Request, Command: t.Command, Moves: toMoved(moves)})
		var eff []store.Effect
		for _, m := range moves {
			eff = append(eff, store.Effect{Action: "moved", Path: m.From, Dest: m.To, Size: sizeOf(m.From), Via: "journal"})
		}
		store.AddEffects(t.ID, eff)
	}
	if t.AzYes {
		cmd += " --yes"
	}
	emit(map[string]string{"status": "run", "cmd": cmd})
}

func cmdDone(args []string) {
	// the plugin reports the exit status of an AI command; logged for training data
	if len(args) < 2 {
		return
	}
	t, err := loadTurn(args[0])
	if err != nil {
		return
	}
	var code int
	fmt.Sscan(args[1], &code)
	store.SetExit(t.ID, code)
}

func cmdShow(args []string) {
	if len(args) < 1 {
		return
	}
	t, err := loadTurn(args[0])
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(t.Command)
	if p := t.Plan; p != nil {
		for i, it := range p.Items {
			if i >= 40 {
				fmt.Printf("  … +%d\n", len(p.Items)-i)
				break
			}
			if it.Dest != "" {
				fmt.Printf("  %s → %s\n", it.Path, it.Dest)
			} else {
				fmt.Printf("  %s\n", it.Path)
			}
		}
		for _, l := range p.Lines {
			fmt.Printf("  %s\n", l)
		}
	}
}

func absPath(p, cwd string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

// ---------------------------------------------------------------- check (debug)

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cwd := fs.String("cwd", "", "")
	_ = fs.Parse(args)
	src := strings.Join(fs.Args(), " ")
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	_ = os.Chdir(*cwd)
	cfg := config.Load()
	L := msg.New(cfg.Language)
	ci := contextinfo.Build(contextinfo.Input{Cwd: *cwd, Language: cfg.Language})
	cat, azDef, sub := azEnv(ci)
	aliases := loadAliases()
	s, err := shell.Parse(src, aliases)
	if err != nil {
		fmt.Println("parse error:", err)
		os.Exit(1)
	}
	home, _ := os.UserHomeDir()
	for _, c := range s.Cmds {
		fmt.Printf("cmd: %s", c.String())
		if c.Alias != "" {
			fmt.Printf("   (alias %s)", c.Alias)
		}
		fmt.Println()
	}
	v := validate.Check(s, validate.Env{OS: ci.OS, Cwd: *cwd, Aliases: aliases, Functions: loadFunctions(), Az: cat, AzDef: azDef})
	for _, p := range v.Problems {
		fmt.Println("problem:", p)
	}
	for _, m := range v.Missing {
		t, _ := tools.Lookup(m)
		fmt.Println("missing:", m, "→", tools.InstallCommand(t, tools.Manager(cfg.PackageManager)))
	}
	for _, w := range v.Warnings {
		fmt.Println("warning:", w)
	}
	r := risk.Classify(s, risk.Env{Home: home, OS: ci.OS, Az: cat, ProtectedSubs: cfg.ProtectedSubscriptions, CurrentSub: sub})
	fmt.Println("risk:", r.Level)
	for _, f := range r.Findings {
		fmt.Printf("  %-7s %s\n", f.Level, f.Why)
	}
	if (r.Level == risk.Change || r.Level == risk.Delete) && testable(s) {
		if res, err := sandbox.Run(src, *cwd, 3*time.Second); err != nil {
			fmt.Println("test run: not run:", err)
		} else {
			c := sandbox.Judge(res, "")
			fmt.Printf("test run: exit %d, timed out %v, denied %v\n  %s\n", res.Exit, res.TimedOut, res.Denied, L.Tested(c))
			for _, p := range c.Problems {
				fmt.Println("  problem:", p)
			}
			res.Close()
		}
	}
	if p := dryrun.Build(s, r, dryrun.Env{Cwd: *cwd, Home: home, Az: cat}); p != nil {
		fmt.Println("question:", L.Question(p, r.Level, *cwd, sub))
		if p.Trashable {
			fmt.Println("  (zshai does this delete itself, to the trash)")
		}
	}
}

// ---------------------------------------------------------------- az catalog

func cmdAzCatalog() {
	py := ""
	for _, p := range []string{"/opt/az/bin/python3"} {
		if _, err := os.Stat(p); err == nil {
			py = p
		}
	}
	if py == "" {
		if out, err := exec.Command("brew", "--prefix", "azure-cli").Output(); err == nil {
			py = filepath.Join(strings.TrimSpace(string(out)), "libexec", "bin", "python")
		}
	}
	if py == "" {
		fmt.Fprintln(os.Stderr, "can't find az's Python (Linux .deb: /opt/az/bin/python3; macOS: brew azure-cli)")
		os.Exit(1)
	}
	script := filepath.Join(config.CacheDir(), "az_extract.py")
	_ = os.MkdirAll(config.CacheDir(), 0o700)
	if err := os.WriteFile(script, azExtractPy, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := azcat.LocalPath()
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	fmt.Fprintln(os.Stderr, "extracting (takes a couple of minutes) …")
	c := exec.Command(py, "-I", script, out)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}

// fixInput decides whether a failed zshai command gets a fix attempt, and returns the
// failed turn and the tail of its error output. No fix for: commands that wrote nothing
// to stderr (grep with no match, diff with differences), Ctrl-C / Ctrl-Z / SIGPIPE,
// fixes of fixes beyond fixAttempts, and fixAttempts = 0.
// errorOutput: error messages that mean the command went wrong even though it exited 0,
// e.g. inside $(( … $(date …) … )) the failing date doesn't change the exit status.
var errorOutput = regexp.MustCompile(`(?im)^usage:|illegal (option|time format|byte sequence)|invalid (option|argument|date|number|time)|` +
	`unrecognized option|unknown option|failed conversion|syntax error|bad substitution|parse error|command not found|bad math expression|` +
	`^\S+: error\b|^error:|compile error|cannot index|cannot iterate|no such file or directory`)

func fixInput(id string, code int, stderrFile string, cfg config.Config) (*turnState, string) {
	if cfg.FixAttempts <= 0 || code == 130 || code == 141 || code == 146 || code == 148 {
		return nil, ""
	}
	t, err := loadTurn(id)
	if err != nil || t.Command == "" || t.Install != "" || t.FixDepth >= cfg.FixAttempts {
		return nil, ""
	}
	b, err := os.ReadFile(stderrFile)
	if err != nil {
		return nil, ""
	}
	text := strings.TrimSpace(string(b))
	if text == "" || (code == 0 && !errorOutput.MatchString(text)) {
		return nil, ""
	}
	if len(text) > 2000 {
		text = "…" + text[len(text)-2000:]
	}
	return t, text
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
