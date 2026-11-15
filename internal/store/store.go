// Package store is zshai's log (PLAN.md §5.1): SQLite at ~/.local/state/zshai/zshai.db
// with psai's tables turns / responses / effects, so the two logs can be read the same
// way. Pure Go (modernc.org/sqlite): the binary still cross-compiles without cgo.
//
// The log is for the user (zshai: siste, zshai log) and for training data. It is never
// needed for zshai to work: every write is best effort and errors are swallowed.
package store

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MariusEng/zshai/internal/config"
)

const SchemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS turns (
  id            INTEGER PRIMARY KEY,
  uid           TEXT NOT NULL UNIQUE,       -- zshai's turn id (20261114-131419-6bfacc41)
  started_at    TEXT NOT NULL,              -- local time, ISO 8601 with UTC offset
  finished_at   TEXT,
  user          TEXT, host TEXT, os TEXT, cwd TEXT,
  lang          TEXT,                       -- en | no
  request       TEXT NOT NULL,              -- what the user typed
  script        TEXT,                       -- final command line
  model         TEXT,
  model_risk    TEXT,                       -- risk label the model gave
  risk          TEXT,                       -- zshai's own classification: read|change|install|delete|danger
  mode          TEXT,                       -- run | show | ask (clarifying question) | install
  outcome       TEXT,                       -- ran | asked-confirm | confirmed | trashed | cancelled | edited | shown | asked | refused | error | install
  exit_code     INTEGER,                    -- reported by the plugin after the command ran
  error         TEXT,
  problems      TEXT,                       -- validation problems sent back to the model, one per line
  created       INTEGER NOT NULL DEFAULT 0,
  changed       INTEGER NOT NULL DEFAULT 0,
  moved         INTEGER NOT NULL DEFAULT 0,
  deleted       INTEGER NOT NULL DEFAULT 0,
  model_seconds REAL,
  total_seconds REAL,
  repairs       INTEGER NOT NULL DEFAULT 0, -- validation failures sent back to the model before showing anything
  fixes         INTEGER NOT NULL DEFAULT 0, -- times the command failed and the model was asked to fix it (as in psai)
  fix_of        TEXT                        -- uid of the failed turn this one fixes
);
CREATE TABLE IF NOT EXISTS responses (       -- every prompt sent to a model and its raw response
  id            INTEGER PRIMARY KEY,
  turn_id       INTEGER NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  at            TEXT NOT NULL,
  model         TEXT,
  system_prompt TEXT,
  prompt        TEXT,
  response      TEXT,
  seconds       REAL,
  error         TEXT
);
CREATE TABLE IF NOT EXISTS effects (         -- what a confirmed command did to files and repos
  id      INTEGER PRIMARY KEY,
  turn_id INTEGER NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
  at      TEXT NOT NULL,
  action  TEXT NOT NULL CHECK (action IN ('created','changed','moved','deleted','backup','restored')),
  path    TEXT NOT NULL,
  dest    TEXT,                               -- new path for moved; trash location for deleted-to-trash; ref for backup
  size    INTEGER,
  via     TEXT                                -- trash | journal | git | undo
);
CREATE INDEX IF NOT EXISTS ix_turns_started ON turns(started_at);
CREATE INDEX IF NOT EXISTS ix_responses_turn ON responses(turn_id);
CREATE INDEX IF NOT EXISTS ix_effects_turn ON effects(turn_id);
`

func Path() string { return filepath.Join(config.StateDir(), "zshai.db") }

// Open opens (and on first use creates) the database. On creation it imports the
// turns.jsonl log written by earlier versions.
func Open() (*sql.DB, error) {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return nil, err
	}
	fresh := false
	if _, err := os.Stat(Path()); os.IsNotExist(err) {
		fresh = true
	}
	db, err := sql.Open("sqlite", "file:"+Path()+"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if fresh {
		_ = os.Chmod(Path(), 0o600)
	}
	var v int
	_ = db.QueryRow("PRAGMA user_version").Scan(&v)
	if v < SchemaVersion {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, err
		}
		_, _ = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion))
		importJSONL(db)
	}
	return db, nil
}

func iso(t time.Time) string { return t.Format("2006-01-02T15:04:05.000-07:00") }

// Response is one model call.
type Response struct {
	At       time.Time
	Prompt   string
	Response string
	Seconds  float64
	Error    string
}

type Turn struct {
	UID, Cwd, Lang, Request, Script, Model, ModelRisk, Risk, Mode, Outcome, Error string
	Started                                                                       time.Time
	Problems                                                                      []string
	ModelSeconds, TotalSeconds                                                    float64
	Repairs                                                                       int
	FixOf                                                                         string
	Responses                                                                     []Response
	SystemPrompt                                                                  string
}

func enabled() bool { return config.Load().Log }

// SaveTurn inserts the turn with its model calls (or replaces it, if the uid exists).
func SaveTurn(t Turn) {
	if !enabled() {
		return
	}
	db, err := Open()
	if err != nil {
		return
	}
	defer db.Close()
	saveTurn(db, t)
}

func saveTurn(db *sql.DB, t Turn) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	u, _ := user.Current()
	uname := ""
	if u != nil {
		uname = u.Username
	}
	host, _ := os.Hostname()
	var id int64
	err = tx.QueryRow(`INSERT INTO turns (uid, started_at, finished_at, user, host, os, cwd, lang, request, script, model,
		model_risk, risk, mode, outcome, error, problems, model_seconds, total_seconds, repairs, fix_of)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET script=excluded.script, risk=excluded.risk, mode=excluded.mode, outcome=excluded.outcome,
		  error=excluded.error, problems=excluded.problems, model_seconds=excluded.model_seconds,
		  total_seconds=excluded.total_seconds, repairs=excluded.repairs
		RETURNING id`,
		t.UID, iso(t.Started), iso(t.Started.Add(time.Duration(t.TotalSeconds*float64(time.Second)))), uname, host, runtime.GOOS,
		t.Cwd, t.Lang, t.Request, t.Script, t.Model, t.ModelRisk, t.Risk, t.Mode, t.Outcome, nullable(t.Error),
		nullable(strings.Join(t.Problems, "\n")), t.ModelSeconds, t.TotalSeconds, t.Repairs, nullable(t.FixOf)).Scan(&id)
	if err != nil {
		return
	}
	for i, r := range t.Responses {
		sys := ""
		if i == 0 {
			sys = t.SystemPrompt
		}
		_, _ = tx.Exec(`INSERT INTO responses (turn_id, seq, at, model, system_prompt, prompt, response, seconds, error)
			VALUES (?,?,?,?,?,?,?,?,?)`, id, i+1, iso(r.At), t.Model, nullable(sys), r.Prompt, r.Response, r.Seconds, nullable(r.Error))
	}
	_ = tx.Commit()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SetOutcome records what the user did with a proposed command (confirmed, cancelled, edited).
func SetOutcome(uid, outcome string) {
	exec1(`UPDATE turns SET outcome=? WHERE uid=?`, outcome, uid)
}

// AddFix counts one fix attempt on the turn whose command failed.
func AddFix(uid string) { exec1(`UPDATE turns SET fixes=fixes+1 WHERE uid=?`, uid) }

// SetExit records the exit status the plugin reported after the command ran.
func SetExit(uid string, code int) {
	exec1(`UPDATE turns SET exit_code=?, finished_at=?, outcome=CASE WHEN outcome IN ('asked-confirm','confirmed') THEN 'ran' ELSE outcome END WHERE uid=?`,
		code, iso(time.Now()), uid)
}

func exec1(q string, args ...any) {
	if !enabled() {
		return
	}
	db, err := Open()
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(q, args...)
}

type Effect struct {
	Action, Path, Dest, Via string
	Size                    int64
}

// AddEffects records what a confirmed turn did, and sets the turn's created/changed/moved/deleted flags.
func AddEffects(uid string, effects []Effect) {
	if !enabled() || len(effects) == 0 {
		return
	}
	db, err := Open()
	if err != nil {
		return
	}
	defer db.Close()
	var id int64
	if db.QueryRow(`SELECT id FROM turns WHERE uid=?`, uid).Scan(&id) != nil {
		return
	}
	now := iso(time.Now())
	for _, e := range effects {
		_, _ = db.Exec(`INSERT INTO effects (turn_id, at, action, path, dest, size, via) VALUES (?,?,?,?,?,?,?)`,
			id, now, e.Action, e.Path, nullable(e.Dest), e.Size, e.Via)
		if col := map[string]string{"created": "created", "changed": "changed", "moved": "moved", "deleted": "deleted"}[e.Action]; col != "" {
			_, _ = db.Exec(`UPDATE turns SET `+col+`=1 WHERE id=?`, id)
		}
	}
}

// Row is a turn as listed by `zshai: siste` / `zshai log`.
type Row struct {
	UID, Started, Cwd, Request, Script, Risk, Mode, Outcome string
	Exit                                                    *int
	Effects                                                 int
	ModelSeconds                                            float64
}

// Last returns the newest turns, newest first. changedOnly keeps turns that moved,
// deleted, created or changed something.
func Last(n int, changedOnly bool) []Row {
	db, err := Open()
	if err != nil {
		return nil
	}
	defer db.Close()
	where := ""
	if changedOnly {
		where = "WHERE created+changed+moved+deleted > 0"
	}
	rows, err := db.Query(`SELECT uid, started_at, cwd, request, COALESCE(script,''), COALESCE(risk,''), COALESCE(mode,''),
		COALESCE(outcome,''), exit_code, (SELECT count(*) FROM effects e WHERE e.turn_id=t.id), COALESCE(model_seconds,0)
		FROM turns t `+where+` ORDER BY started_at DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var exit sql.NullInt64
		if rows.Scan(&r.UID, &r.Started, &r.Cwd, &r.Request, &r.Script, &r.Risk, &r.Mode, &r.Outcome, &exit, &r.Effects, &r.ModelSeconds) == nil {
			if exit.Valid {
				e := int(exit.Int64)
				r.Exit = &e
			}
			out = append(out, r)
		}
	}
	return out
}

// Detail is one turn with its model calls and effects (`zshai log --id`).
func Detail(uid string) (Row, []Response, []Effect, bool) {
	db, err := Open()
	if err != nil {
		return Row{}, nil, nil, false
	}
	defer db.Close()
	var r Row
	var id int64
	var exit sql.NullInt64
	err = db.QueryRow(`SELECT id, uid, started_at, cwd, request, COALESCE(script,''), COALESCE(risk,''), COALESCE(mode,''),
		COALESCE(outcome,''), exit_code, COALESCE(model_seconds,0) FROM turns WHERE uid=? OR uid LIKE ? ORDER BY id DESC LIMIT 1`,
		uid, "%"+uid).Scan(&id, &r.UID, &r.Started, &r.Cwd, &r.Request, &r.Script, &r.Risk, &r.Mode, &r.Outcome, &exit, &r.ModelSeconds)
	if err != nil {
		return Row{}, nil, nil, false
	}
	if exit.Valid {
		e := int(exit.Int64)
		r.Exit = &e
	}
	var resp []Response
	if rows, err := db.Query(`SELECT at, COALESCE(prompt,''), COALESCE(response,''), COALESCE(seconds,0), COALESCE(error,'') FROM responses WHERE turn_id=? ORDER BY seq`, id); err == nil {
		for rows.Next() {
			var x Response
			var at string
			if rows.Scan(&at, &x.Prompt, &x.Response, &x.Seconds, &x.Error) == nil {
				x.At, _ = time.Parse("2006-01-02T15:04:05.000-07:00", at)
				resp = append(resp, x)
			}
		}
		rows.Close()
	}
	var eff []Effect
	if rows, err := db.Query(`SELECT action, path, COALESCE(dest,''), COALESCE(size,0), COALESCE(via,'') FROM effects WHERE turn_id=? ORDER BY id`, id); err == nil {
		for rows.Next() {
			var e Effect
			if rows.Scan(&e.Action, &e.Path, &e.Dest, &e.Size, &e.Via) == nil {
				eff = append(eff, e)
			}
		}
		rows.Close()
	}
	return r, resp, eff, true
}

// importJSONL moves the turns.jsonl log of earlier versions into the database once.
func importJSONL(db *sql.DB) {
	p := filepath.Join(config.StateDir(), "turns.jsonl")
	f, err := os.Open(p)
	if err != nil {
		return
	}
	type old struct {
		ID       string    `json:"id"`
		Start    time.Time `json:"start"`
		Cwd      string    `json:"cwd"`
		Request  string    `json:"request"`
		Model    string    `json:"model"`
		Prompt   string    `json:"prompt"`
		Raw      []string  `json:"raw"`
		Command  string    `json:"command"`
		Risk     string    `json:"risk"`
		Outcome  string    `json:"outcome"`
		Exit     *int      `json:"exit"`
		Problems []string  `json:"problems"`
		ModelMs  int64     `json:"modelMs"`
		TotalMs  int64     `json:"totalMs"`
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var exits []old
	for sc.Scan() {
		var o old
		if json.Unmarshal(sc.Bytes(), &o) != nil || o.ID == "" {
			continue
		}
		if o.Outcome == "exit" {
			exits = append(exits, o)
			continue
		}
		t := Turn{UID: o.ID, Started: o.Start, Cwd: o.Cwd, Request: o.Request, Model: o.Model, Script: o.Command, Risk: o.Risk,
			Outcome: o.Outcome, Problems: o.Problems, ModelSeconds: float64(o.ModelMs) / 1000, TotalSeconds: float64(o.TotalMs) / 1000,
			Repairs: max(len(o.Raw)-1, 0)}
		for i, r := range o.Raw {
			prompt := ""
			if i == 0 {
				prompt = o.Prompt
			}
			t.Responses = append(t.Responses, Response{At: o.Start, Prompt: prompt, Response: r})
		}
		saveTurn(db, t)
	}
	f.Close()
	for _, o := range exits {
		if o.Exit != nil {
			_, _ = db.Exec(`UPDATE turns SET exit_code=?, outcome=CASE WHEN outcome IN ('asked-confirm','confirmed') THEN 'ran' ELSE outcome END WHERE uid=?`, *o.Exit, o.ID)
		}
	}
	_ = os.Rename(p, p+".imported")
}

type Summary struct {
	Turns, Ran, Failed, Cancelled, Edited, Shown, Asked, Fixes, Refused, Undone int
	Since                                                                       string
	AvgModel, MedianModel                                                       float64
	TopTools                                                                    []string // most used programs in zshai's commands
}

// Stats summarises the log for `zshai: stats`.
func Stats() (Summary, bool) {
	db, err := Open()
	if err != nil {
		return Summary{}, false
	}
	defer db.Close()
	var s Summary
	var since sql.NullString
	err = db.QueryRow(`SELECT count(*), min(started_at),
		COALESCE(sum(exit_code = 0), 0), COALESCE(sum(exit_code <> 0), 0),
		COALESCE(sum(outcome = 'cancelled'), 0), COALESCE(sum(outcome = 'edited'), 0), COALESCE(sum(outcome = 'shown'), 0),
		COALESCE(sum(outcome = 'asked'), 0), COALESCE(sum(fixes), 0), COALESCE(sum(outcome = 'refused'), 0),
		COALESCE(avg(model_seconds), 0) FROM turns`).Scan(&s.Turns, &since, &s.Ran, &s.Failed, &s.Cancelled, &s.Edited,
		&s.Shown, &s.Asked, &s.Fixes, &s.Refused, &s.AvgModel)
	if err != nil {
		return Summary{}, false
	}
	if since.Valid && len(since.String) >= 10 {
		s.Since = since.String[:10]
	}
	_ = db.QueryRow(`SELECT count(DISTINCT turn_id) FROM effects WHERE action = 'restored'`).Scan(&s.Undone)
	if s.Turns > 0 {
		_ = db.QueryRow(`SELECT COALESCE(model_seconds, 0) FROM turns ORDER BY model_seconds LIMIT 1 OFFSET ?`, s.Turns/2).Scan(&s.MedianModel)
	}
	rows, err := db.Query(`SELECT script FROM turns WHERE script <> ''`)
	if err == nil {
		count := map[string]int{}
		for rows.Next() {
			var sc string
			if rows.Scan(&sc) == nil {
				seen := map[string]bool{}
				for _, part := range strings.FieldsFunc(sc, func(r rune) bool { return r == '|' || r == ';' || r == '&' || r == '\n' }) {
					f := strings.Fields(part)
					for len(f) > 0 && (f[0] == "sudo" || strings.Contains(f[0], "=")) {
						f = f[1:]
					}
					if len(f) > 0 && !seen[f[0]] {
						seen[f[0]] = true
						count[f[0]]++
					}
				}
			}
		}
		rows.Close()
		type kv struct {
			k string
			v int
		}
		var all []kv
		for k, v := range count {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
		for i := 0; i < len(all) && i < 6; i++ {
			s.TopTools = append(s.TopTools, fmt.Sprintf("%s (%d)", all[i].k, all[i].v))
		}
	}
	return s, true
}
