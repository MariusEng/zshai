package validate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPSTable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "salg.csv"), []byte("dato,vare,beløp\n2026-01-01,kaffe,45\n"), 0o644)
	cases := map[string]string{
		`jq -r '(.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' orders.json | column -t -s $'\t'`:                          `zshai fmt -t orders.json`,
		`jq -s -r 'sort_by(-.mem) | .[:3] | (.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' ps.jsonl | column -t -s $'\t'`: `jq -s 'sort_by(-.mem) | .[:3]' ps.jsonl | zshai fmt -t`,
		`column -s, -t < sales.csv`: `zshai fmt -t sales.csv`,
		`column -t -s';' data.csv`:  `zshai fmt -t data.csv`,
		`(head -n 1 salg.csv; tail -n +2 salg.csv | sort -t, -k3 -nr) | column -s, -t`: `zshai fmt -t --sort -beløp salg.csv`,
		// not the recipes: left alone
		`jq -r '.[] | [.a, .b] | @tsv' x.json | column -t`: ``,
		`column -t -s $'\t' x.tsv | sort`:                  ``,
	}
	for in, want := range cases {
		if got := PSTable(in, dir); got != want {
			t.Errorf("%s\n got %q\nwant %q", in, got, want)
		}
	}
}
