// Package journal records what zshai moved, trashed or backed up, so `angre`/`undo`
// can reverse the last AI action, and keeps a log of every turn.
package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/store"
	"github.com/MariusEng/zshai/internal/trash"
)

type Entry struct {
	ID      string        `json:"id"`
	Time    time.Time     `json:"time"`
	Kind    string        `json:"kind"` // trash | move | git-backup
	Request string        `json:"request"`
	Command string        `json:"command"`
	Moves   []trash.Moved `json:"moves,omitempty"`
	Repo    string        `json:"repo,omitempty"`
	Head    string        `json:"head,omitempty"`
	Stash   string        `json:"stash,omitempty"`
	Ref     string        `json:"ref,omitempty"`
	Branch  string        `json:"branch,omitempty"`
	Undone  bool          `json:"undone,omitempty"`
}

func path() string { return filepath.Join(config.StateDir(), "journal.jsonl") }

func Append(e Entry) error {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	return err
}

func All() []Entry {
	f, err := os.Open(path())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// LastUndoable returns the newest entry not undone yet (an undo is recorded as a
// second line with the same id and Undone=true).
func LastUndoable() (Entry, bool) {
	all := All()
	undone := map[string]bool{}
	for _, e := range all {
		if e.Undone {
			undone[e.ID] = true
		}
	}
	for i := len(all) - 1; i >= 0; i-- {
		if !all[i].Undone && !undone[all[i].ID] {
			return all[i], true
		}
	}
	return Entry{}, false
}

func MarkUndone(e Entry) error {
	e.Undone = true
	e.Time = time.Now()
	return Append(e)
}

// Turn is one AI turn in the log.
// Turn collects one request while the helper works on it; LogTurn writes it to the
// SQLite log (internal/store).
type Turn struct {
	ID, Cwd, Request, Model, Lang, Mode string
	FixOf                               string
	Start                               time.Time
	Command, Risk, ModelRisk, Outcome   string
	Error                               string
	Problems                            []string
	Calls                               []store.Response
	ModelMs, TotalMs                    int64
}

func LogTurn(t Turn) {
	store.SaveTurn(store.Turn{UID: t.ID, Started: t.Start, Cwd: t.Cwd, Lang: t.Lang, Request: t.Request, Script: t.Command,
		Model: t.Model, ModelRisk: t.ModelRisk, Risk: t.Risk, Mode: t.Mode, Outcome: t.Outcome, Error: t.Error,
		Problems: t.Problems, ModelSeconds: float64(t.ModelMs) / 1000, TotalSeconds: float64(t.TotalMs) / 1000,
		Repairs: max(len(t.Calls)-1, 0), FixOf: t.FixOf, Responses: t.Calls, SystemPrompt: llmSystem})
}

// llmSystem is set by the helper (journal must not import llm's client).
var llmSystem string

func SetSystemPrompt(s string) { llmSystem = s }
