package typo

import "testing"

// a typical PATH: the common commands plus some rarely used tools that shouldn't win
var names = append(append([]string{}, Common...),
	"rg", "rgrep", "rn2", "lsof", "lsblk", "lz4", "gtk-launch", "gio", "vipw", "visudo", "la", "ll", "l")

func TestSuggest(t *testing.T) {
	extra := map[string]bool{"la": true, "ll": true, "gst": true}
	nm := append(append([]string{}, names...), "gst")
	for in, want := range map[string]string{
		"rn foo.txt":          "rm foo.txt",
		"sl":                  "ls",
		"sl -la":              "ls -la",
		"gti status":          "git status",
		"got status":          "git status",
		"gerp -r TODO .":      "grep -r TODO .",
		"grpe TODO notes.txt": "grep TODO notes.txt",
		"claer":               "clear",
		"pyhton3 app.py":      "python3 app.py",
		"mkdri new":           "mkdir new",
		"ehco hi":             "echo hi",
		"cd..":                "cd ..",
		"ls-la":               "ls -la",
		"LS":                  "ls",
		"sudo atp install jq": "sudo apt install jq",
		"  cta notes.txt":     "  cat notes.txt",
		"cs Downloads":        "cd Downloads",
		"tial -f app.log":     "tail -f app.log",
		"lss":                 "ls",
		"kubctl get pods":     "kubectl get pods",
		"dokcer ps":           "docker ps",
		"vmi notes.txt":       "vim notes.txt",
		"suod apt update":     "sudo apt update",
		"histroy":             "history",
		"chmdo +x run.sh":     "chmod +x run.sh",
	} {
		f, ok := Suggest(in, nm, extra)
		if !ok || f.Line != want {
			t.Errorf("Suggest(%q) = %q, %v; want %q", in, f.Line, ok, want)
		}
	}
}

func TestNoGuess(t *testing.T) {
	for _, in := range []string{
		"ls -la",                         // a command
		"vis filene i mappa",             // Norwegian request
		"lag en fil med navn",            // request
		"show me the biggest files",      // request
		"hvor er det jeg lagret bildene", // request
		"xyzzy",                          // nothing close
		"q",                              // one letter: too little to go on
		"./build.sh",                     // a path
		"FOO=1 make",                     // assignment
		"shwo me all pdfs",               // a request with a typo: the model gets it
		"lsit all files",                 // likewise
	} {
		if f, ok := Suggest(in, names, nil); ok {
			t.Errorf("Suggest(%q) = %q, want no suggestion", in, f.Line)
		}
	}
}

func TestDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"rn", "rm", 0.5}, {"sl", "ls", 0.5}, {"gti", "git", 0.5}, {"LS", "ls", 0.2},
		{"lss", "ls", 0.5}, {"rx", "rm", 1}, {"git", "git", 0},
	} {
		if got := Distance(c.a, c.b); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("Distance(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
