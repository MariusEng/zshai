package psfmt

import (
	"strings"
	"testing"
)

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	v, err := ParseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTable(t *testing.T) {
	v := mustJSON(t, `[{"name":"Alice","age":30,"city":"Oslo"},{"name":"Bob","age":4,"city":"Bergen"}]`)
	got := Format(v, Options{})
	want := "\nname  age city\n----  --- ----\nAlice  30 Oslo\nBob     4 Bergen\n\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestListForMoreThanFourProps(t *testing.T) {
	v := mustJSON(t, `{"id":1,"name":"x","tags":["a","b","c","d","e"],"addr":{"zip":"0150","city":"Oslo"},"ok":true}`)
	got := Format(v, Options{})
	want := "\nid   : 1\nname : x\ntags : {a, b, c, d…}\naddr : @{zip=0150; city=Oslo}\nok   : True\n\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestJSONLinesSortFirstProps(t *testing.T) {
	v := mustJSON(t, "{\"n\":\"a\",\"mem\":5}\n{\"n\":\"b\",\"mem\":50}\n{\"n\":\"c\",\"mem\":7}\n")
	got := Format(v, Options{Mode: Table, Sort: "-MEM", First: 2, Props: []string{"n"}})
	want := "\nn\n-\nb\nc\n\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCSV(t *testing.T) {
	rows, err := ParseCSV([]byte("dato;vare;beløp\n2026-01-01;Kaffe;45\n2026-01-02;Brød;120\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	got := Format(rows, Options{Mode: Table, Sort: "-beløp"})
	if !strings.Contains(got, "dato       vare  beløp\n") || !strings.Contains(got, "2026-01-02 Brød    120\n") {
		t.Errorf("got\n%s", got)
	}
}

func TestShrinkToWidth(t *testing.T) {
	v := mustJSON(t, `[{"a":"`+strings.Repeat("x", 100)+`","b":1}]`)
	for _, l := range strings.Split(Format(v, Options{Mode: Table, Width: 40}), "\n") {
		if width(l) > 39 {
			t.Errorf("line too wide (%d): %s", width(l), l)
		}
	}
}
