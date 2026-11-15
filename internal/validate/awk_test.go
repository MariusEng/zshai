package validate

import (
	"strings"
	"testing"

	"github.com/MariusEng/zshai/internal/shell"
)

func TestAwk(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{`brew outdated --quiet | wc -l | awk '{print $1 > 0 ? "Ja" : "Nei"}'`, true},
		{`awk '{print ($1 > 0 ? "Ja" : "Nei")}' f`, false},
		{`awk -F, '{print $2 > "out.txt"}' f`, false},
		{`awk '{print $1}' f`, false},
		{`awk -F: -v x=1 '$3 > 100 {print $1}' /etc/passwd`, false},
		{`awk '{print $1' f`, true}, // unbalanced brace: real awk reports it
		{`awk 'BEGIN{print "x"}'`, false},
	}
	for _, c := range cases {
		s, err := shell.Parse(c.src, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		var got []string
		for _, cmd := range s.Cmds {
			got = append(got, awkCheck(cmd)...)
		}
		if (len(got) > 0) != c.want {
			t.Errorf("%s: got %v, want problem=%v", c.src, got, c.want)
		}
	}
}

func TestGawkOnly(t *testing.T) {
	s, _ := shell.Parse(`awk -F, 'NR>1{a[NR]=$4} END{asort(a); for(i in a) print a[i]}' sales.csv`, nil)
	got := awkCheck(s.Cmds[0])
	if isGawk("awk") {
		if len(got) > 0 {
			t.Errorf("gawk has asort: %v", got)
		}
		return
	}
	if len(got) == 0 || !strings.Contains(got[0], "asort") {
		t.Errorf("non-GNU awk: asort not flagged: %v", got)
	}
}
