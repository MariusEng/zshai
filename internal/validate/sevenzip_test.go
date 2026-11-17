package validate

import (
	"errors"
	"testing"
)

func TestSevenZip(t *testing.T) {
	defer func(f func(string) (string, error)) { lookPath = f }(lookPath)
	on := func(names ...string) {
		lookPath = func(n string) (string, error) {
			for _, x := range names {
				if x == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	on("7zz") // a Mac with Homebrew's sevenzip
	for in, want := range map[string]string{
		"7z x backup.7z":                       "7zz x backup.7z",
		"7z x backup.7z -o out":                "7zz x backup.7z -oout",
		"7z x -o 'my files' backup.7z":         "7zz x -o'my files' backup.7z",
		"7z a archive.7z Documents/":           "7zz a archive.7z Documents/",
		"mkdir -p out && 7z x a.7z -o out":     "mkdir -p out && 7zz x a.7z -oout",
		"find . -name '*.7z' | xargs -n1 7z t": "find . -name '*.7z' | xargs -n1 7zz t",
		"7zz x a.7z -oout":                     "",
		"7zz l a.7z":                           "",
		"tar -xzf a.tgz -C out":                "",
		"7z x a.7z -o out && ls -o x":          "7zz x a.7z -oout && ls -o x",
		"echo 7z is great":                     "",
	} {
		if got := SevenZip(in); got != want {
			t.Errorf("SevenZip(%q) = %q, want %q", in, got, want)
		}
	}
	on("7z") // Linux with p7zip
	if got := SevenZip("7zz x a.7z"); got != "7z x a.7z" {
		t.Errorf("7zz on a 7z machine: %q", got)
	}
	on() // none: leave it for the "not installed" check and the install offer
	if got := SevenZip("7z x a.7z"); got != "" {
		t.Errorf("nothing installed: %q", got)
	}
}
