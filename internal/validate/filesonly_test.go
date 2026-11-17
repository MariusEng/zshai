package validate

import "testing"

func TestFilesOnly(t *testing.T) {
	for in, want := range map[string]string{
		"rm -f zshai*":              "rm -- zshai*(.)",
		"rm -f zshai*.* zshai*":     "rm -- zshai*.*(.) zshai*(.)",
		"rm -rfv -- build*":         "rm -v -- build*(.)",
		"rm zshai-a.txt":            "",
		"rm -f zshai* && ls":        "",
		"find . -name 'x*' -delete": "",
		"rm \"$f\"*":                "",
	} {
		if got := FilesOnly(in); got != want {
			t.Errorf("FilesOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
