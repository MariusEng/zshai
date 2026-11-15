package validate

import (
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestTrimmed(t *testing.T) {
	cases := []struct {
		req, cmd string
		want     bool
	}{
		{"hvor lenge har maskinen vært oppe", `uptime | awk '{print $3}' | sed 's/,//'`, true},
		{"hvor mye plass er ledig på disken", `df -h | grep '^/dev/' | awk '{print $4}' | head -1`, true},
		{"how much memory is free", `free -h | grep Mem`, true},
		{"hvor mye plass er ledig på server", `ssh server 'df -h .' | awk 'NR==2 {print $4}'`, true},
		{"hvor lenge har maskinen vært oppe", `uptime`, false},
		{"vis bare antall dager maskinen har vært oppe", `uptime | awk '{print $3}'`, false},
		{"how many users are logged in", `w | tail -n +3 | wc -l`, false},
		{"vis de 5 største filene", `du -ah . | sort -rh | head -5`, false},
	}
	for _, c := range cases {
		s, err := shell.Parse(c.cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(Trimmed(s, c.req)) > 0; got != c.want {
			t.Errorf("%q / %q: got %v, want %v", c.req, c.cmd, got, c.want)
		}
	}
}

func TestLsTotal(t *testing.T) {
	for cmd, want := range map[string]bool{
		"ls -lt | head -n 3": true, "ls -lSh | head -5": true, "ls -t | head -n 3": false,
		"ls -lSh | sed -n 2,6p": false, "ls -ld */ | head -3": false,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(LsTotal(s)) > 0; got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
}

func TestYesNo(t *testing.T) {
	for cmd, want := range map[string]bool{
		`brew outdated --quiet | wc -l | awk '{print($1 > 0 ? "Ja" : "Nei")}'`: true,
		`if [ -n "$(brew outdated)" ]; then echo yes; else echo no; fi`:        true,
		`brew outdated --quiet`:  false,
		`grep -c "no" notes.txt`: false,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(YesNo(s)) > 0; got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
}

func TestRemotePathAndQuotedNames(t *testing.T) {
	s, err := shell.Parse(`ssh alice@server 'du -sh /Users/alice/git/tv'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := RemotePath(s, "/Users/alice", "/Users/alice/git/tv"); len(p) == 0 || !strings.Contains(p[0], "~/git/tv") {
		t.Errorf("RemotePath: %v", p)
	}
	if p := QuotedNames(s, `Hvor stort er prosjektet "zshai" som ligger på server?`); len(p) == 0 {
		t.Error("QuotedNames missed zshai")
	}
	if p := RemotePath(s, "/somewhere/else", "/tmp"); len(p) == 0 {
		t.Error("RemotePath: /Users/… on a remote host")
	}
	cwdRemote, _ := shell.Parse(`ssh server 'find ~/git/tv -name zshai -type d'`, nil)
	if p := RemotePath(cwdRemote, "/Users/alice", "/Users/alice/git/tv"); len(p) == 0 {
		t.Error("RemotePath: local cwd used on the remote host")
	}
	local, _ := shell.Parse(`du -sh ~/zshai-test/live/tv`, nil)
	if p := QuotedNames(local, `prosjektet "zshai"`); len(p) == 0 {
		t.Error("QuotedNames: zshai-test is not zshai")
	}
	ok, _ := shell.Parse(`ssh alice@server 'du -sh ~/git/zshai'`, nil)
	if p := append(RemotePath(ok, "/Users/alice", "/Users/alice/git/tv"), QuotedNames(ok, `prosjektet "zshai"`)...); len(p) > 0 {
		t.Errorf("false positive: %v", p)
	}
}

func TestBSDps(t *testing.T) {
	for cmd, want := range map[string]int{
		"ps -eo pid,ppid,cmd --no-headers": 2, "ps -axo pid=,ppid=,command=": 0, "ps aux --sort=-%mem": 1, "ps -axm": 0,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := flagTraps(s.Cmds[0], "darwin"); len(got) != want {
			t.Errorf("%q: got %v, want %d problems", cmd, got, want)
		}
	}
}

func TestSortAfterTable(t *testing.T) {
	for cmd, want := range map[string]bool{
		`jq -s -r '[.pid] | @tsv' p.json | column -t -s $'\t' | sort -k4 -nr`:         true,
		`jq -r '[.pid,.mem] | @tsv' p.json | sort -t$'\t' -k2 -nr | head -n 4`:        true,
		`jq -s -r 'sort_by(-.mem) | .[] | [.pid] | @tsv' p.json | column -t -s $'\t'`: false,
		`(head -n 1 f.csv; tail -n +2 f.csv | sort -t, -k4 -nr) | column -s, -t`:      false,
		`awk '{print $3}' app.log | sort | uniq -c | sort -rn`:                        false,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(SortAfterTable(s)) > 0; got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
}

func TestCSVSortHeader(t *testing.T) {
	for cmd, want := range map[string]bool{
		`sort -t, -k4 -nr sales.csv | column -s, -t`:                                     true,
		`(head -n 1 sales.csv; tail -n +2 sales.csv | sort -t, -k4 -nr) | column -s, -t`: false,
		`awk -F, 'NR>1 {print $4, $1}' sales.csv | sort -nr | head -2`:                   false,
		`sort notes.txt`: false,
	} {
		s, err := shell.Parse(cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(CSVSortHeader(s)) > 0; got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
}

func TestTableAsked(t *testing.T) {
	no, _ := shell.Parse(`jq -r 'select(.user == "root") | (.pid, .user)' p.json`, nil)
	if len(TableAsked(no, "vis prosessene til root som tabell")) == 0 {
		t.Error("TableAsked missed a non-table")
	}
	yes, _ := shell.Parse(`jq -r '[.pid] | @tsv' p.json | column -t -s $'\t'`, nil)
	if len(TableAsked(yes, "vis som tabell")) > 0 || len(TableAsked(no, "vis prosessene")) > 0 {
		t.Error("TableAsked false positive")
	}
}
