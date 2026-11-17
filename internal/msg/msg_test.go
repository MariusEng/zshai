package msg

import (
	"testing"

	"github.com/MariusEng/zshai/internal/sandbox"
)

func TestTested(t *testing.T) {
	c := sandbox.Check{Files: []sandbox.FileNote{
		{Effect: sandbox.Effect{Kind: "created", Path: "navn.json", Size: 90}, Items: 5, Format: "json", Valid: true},
		{Effect: sandbox.Effect{Kind: "moved", From: "logs", Path: "arkiv/logs", IsDir: true}, Items: -1},
		{Effect: sandbox.Effect{Kind: "modified", Path: "salg.csv", Size: 20}, Items: 1, Format: "csv", Valid: true},
	}}
	want := "Testkjørt i en kopi av mappa: lager navn.json (5 elementer, gyldig JSON); flytter logs/ → arkiv/logs/; endrer salg.csv (1 rad, gyldig CSV)."
	if got := New("no").Tested(c); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
