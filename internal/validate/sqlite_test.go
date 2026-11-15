package validate

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestSqliteCheck(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "x.db")
	h, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`CREATE TABLE turns (id INTEGER, request TEXT)`); err != nil {
		t.Fatal(err)
	}
	h.Close()
	cases := []struct {
		src  string
		want string
	}{
		{`sqlite3 x.db "SELECT COUNT(*) FROM turns"`, ""},
		{`sqlite3 -readonly x.db "SELECT SUM(size) FROM logs;"`, "no such table: logs"},
		{`sqlite3 x.db "SELECT LENGTH(content) FROM turns"`, "no such column: content"},
		{`sqlite3 x.db "DELETE FROM nothing"`, ""}, // writes are never compiled here
		{`sqlite3 missing.db "SELECT 1 FROM t"`, "does not exist"},
		{`echo "SELECT request, duration FROM turns" | sqlite3 x.db`, "no such column: duration"},
	}
	for _, c := range cases {
		s, err := shell.Parse(c.src, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(sqliteCheck(s.Cmds[len(s.Cmds)-1], dir), " | ")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.src, got, c.want)
		}
		if strings.Contains(c.want, "no such") && !strings.Contains(got, "turns(id, request)") {
			t.Errorf("%s: no table list in %q", c.src, got)
		}
	}
}
