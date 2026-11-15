package validate

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/MariusEng/zshai/internal/shell"
)

// sqlite3 options that take a value (sqlite3 -help, 3.4x)
var sqliteValueOpts = map[string]bool{
	"-cmd": true, "-separator": true, "-newline": true, "-nullvalue": true, "-init": true,
	"-maxsize": true, "-mmap": true, "-pagecache": true, "-lookaside": true, "-vfs": true,
	"-heap": true, "-escape": true,
}

var sqliteRead = regexp.MustCompile(`(?is)^\s*(select|with)\b`)

// sqliteArgs finds the database file and the SQL in  sqlite3 [options] FILE "SQL".
func sqliteArgs(c *shell.Cmd) (db, query string) {
	var pos []string
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if strings.HasPrefix(a, "-") && c.Lit[i] {
			if sqliteValueOpts[a] || sqliteValueOpts["-"+strings.TrimLeft(a, "-")] {
				i++
			}
			continue
		}
		if !c.Lit[i] {
			return "", ""
		}
		pos = append(pos, a)
	}
	switch {
	case len(pos) == 2:
		return pos[0], pos[1]
	case len(pos) == 1 && c.PipeFrom != nil:
		// echo "SELECT …" | sqlite3 db
		if s, err := shell.Parse(shell.Print(c.PipeFrom), nil); err == nil && len(s.Cmds) == 1 {
			e := s.Cmds[0]
			if (e.Name == "echo" || e.Name == "printf") && len(e.Args) == 1 && e.Lit[0] {
				return pos[0], e.Args[0]
			}
		}
	}
	return "", ""
}

// sqliteCheck compiles a read query against the real database, read-only, without running
// it: an invented table or column ("no such column: content") goes back to the model.
func sqliteCheck(c *shell.Cmd, cwd string) []string {
	if c.Name != "sqlite3" {
		return nil
	}
	db, query := sqliteArgs(c)
	if db == "" || db == ":memory:" || !sqliteRead.MatchString(query) {
		return nil
	}
	written := db
	if strings.HasPrefix(db, "~/") {
		home, _ := os.UserHomeDir()
		db = filepath.Join(home, db[2:])
	} else if !filepath.IsAbs(db) {
		db = filepath.Join(cwd, db)
	}
	fi, err := os.Stat(db)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"sqlite3: the database " + written + " does not exist; use the path shown in the context"}
		}
		return nil
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	h, err := sql.Open("sqlite", "file:"+(&url.URL{Path: db}).EscapedPath()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil
	}
	defer h.Close()
	var problems []string
	for _, stmt := range strings.Split(query, ";") {
		if strings.TrimSpace(stmt) == "" || !sqliteRead.MatchString(stmt) {
			continue
		}
		st, err := h.Prepare(stmt)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "no such") || strings.Contains(msg, "syntax error") || strings.Contains(msg, "ambiguous") {
				problems = append(problems, "sqlite3: "+msg+tables(h))
			}
			continue
		}
		st.Close()
	}
	return problems
}

// tables lists the database's tables and columns, so the model can fix the query.
func tables(h *sql.DB) string {
	rows, err := h.Query(`SELECT m.name, group_concat(p.name, ', ') FROM sqlite_schema m, pragma_table_info(m.name) p
		WHERE m.type IN ('table','view') AND m.name NOT LIKE 'sqlite_%' GROUP BY m.name ORDER BY m.name`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var t, cols string
		if rows.Scan(&t, &cols) == nil {
			parts = append(parts, t+"("+cols+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, "; ")
	if len(s) > 1500 {
		s = s[:1500] + "…"
	}
	return " (tables: " + s + ")"
}
