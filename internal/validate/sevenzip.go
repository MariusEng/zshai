package validate

import (
	"os/exec"
	"regexp"
	"strings"
)

// 7-Zip goes by several names: 7z (Linux p7zip/7zip packages), 7zz (the official build;
// Homebrew's sevenzip on macOS, the 7zip package on newer Ubuntu), 7za. Models write 7z
// almost always. SevenZip fixes the command without asking the model again:
//   - the name becomes the one that is installed here (7z → 7zz on a Mac);
//   - "-o dir" becomes "-odir": 7-Zip takes the output folder glued to the switch, and
//     "-o out" is a "Command Line Error" (or, worse, "out" read as a file to add).
// It returns "" when nothing changed.

var (
	sevenRe   = regexp.MustCompile(`(^|[|;&(\x60]\s*|\b(?:sudo|xargs|time|nice|nohup|command)\s+(?:-\S+\s+)*)(7zz|7za|7z)(\s|$)`)
	sevenSegm = regexp.MustCompile(`\b(?:7zz|7za|7z)\s[^|;&\n]*`)
	spacedO   = regexp.MustCompile(`(\s-o)\s+('[^']*'|"[^"]*"|[^\s'"|;&-][^\s|;&]*)`)
)

var lookPath = exec.LookPath

// SevenZipName: the 7-Zip binary installed here, preferring want; "" if there is none.
func SevenZipName(want string) string {
	for _, n := range append([]string{want}, "7zz", "7z", "7za") {
		if n == "" {
			continue
		}
		if _, err := lookPath(n); err == nil {
			return n
		}
	}
	return ""
}

func SevenZip(src string) string {
	if !strings.Contains(src, "7z") {
		return ""
	}
	out := sevenRe.ReplaceAllStringFunc(src, func(m string) string {
		sub := sevenRe.FindStringSubmatch(m)
		have := SevenZipName(sub[2])
		if have == "" || have == sub[2] {
			return m
		}
		return sub[1] + have + sub[3]
	})
	out = sevenSegm.ReplaceAllStringFunc(out, func(seg string) string {
		return spacedO.ReplaceAllString(seg, "$1$2")
	})
	if out == src {
		return ""
	}
	return out
}
