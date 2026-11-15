package router

import (
	"os"
	"path/filepath"
	"testing"
)

// Commands as the plugin would see them: PATH + builtins + oh-my-zsh aliases.
var known = map[string]bool{}

func init() {
	for _, w := range []string{"ls", "cd", "find", "sort", "kill", "cat", "git", "gst", "gco", "gp", "grhh", "brew",
		"cargo", "ssh", "ping", "scp", "sudo", "claude", "psai", "pwsh", "code", "fastfetch", "mkdir", "nano", "exit",
		"source", "clear", "cp", "curl", "hostname", "az", "make", "rm", "mv", "echo", "grep", "du", "df", "open",
		"l", "ll", "la", "1", "2", "3", "-", "which", "man", "top", "show", "list", "convert", "time", "date", "head",
		"tail", "wc", "less", "vim", "python3", "node", "npm", "docker", "kubectl", "tar", "zip", "unzip", "touch"} {
		known[w] = true
	}
}

func env(t *testing.T) Env {
	dir := t.TempDir()
	for _, d := range []string{"Pictures", "src"} {
		os.Mkdir(filepath.Join(dir, d), 0o755)
	}
	for _, f := range []string{"sales.csv", "notes.txt", "all"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	return Env{Cwd: dir, IsCommand: func(w string) bool { return known[w] }}
}

func TestShellLinesStayShell(t *testing.T) {
	e := env(t)
	// From the Mac's history ranking and everyday shell use: none may go to the AI.
	lines := []string{
		"ls", "ls -la", "cd ~/git/zshai", "cd ..", "gst", "gco main", "gp", "grhh", "git push", "git status",
		"brew update", "brew upgrade", "brew install ffmpeg", "brew search kitty", "brew upadte",
		"cargo run", "cargo build --release", "ssh llmhost", "ssh alice@192.0.2.10", "ping llmhost", "ping -c 3 1.1.1.1",
		"scp file.txt llmhost:", "sudo apt update", "claude", "psai", "pwsh", "code .", "fastfetch", "mkdir test",
		"nano ~/.zshrc", "exit", "source ~/.zshrc", "clear", "cp a b", "curl -s https://example.com", "hostname",
		"az login", "az vm list -o table", "az group list", "make", "rm notes.txt", "mv a b",
		"69", "70", "10M35", "16M35", "4M35", "2", "1", "-",
		"Pictures", "src", "./run.sh", "~/bin/x", "FOO=1 make",
		"find . -name '*.pdf'", "sort sales.csv", "cat notes.txt", "kill 1234", "echo hello world",
		"git commit -m 'fix the thing'", "echo 'what is this'", "grep -r TODO src",
		"open https://example.com", "man ls", "which git", "time make", "du -sh *", "df -h",
		"find all", // 'all' is an existing file here
		"cat sales.csv | wc -l", "ls *.jpg", "echo $HOME",
	}
	for _, l := range lines {
		if d := Decide(l, e); d.AI {
			t.Errorf("shell line sent to AI: %q", l)
		}
	}
}

func TestRequestsGoToAI(t *testing.T) {
	e := env(t)
	lines := []string{
		// first word is not a command
		"hvor mange rader er det i sales.csv?",
		"lag en mappe for de nyeste bildene mine og flytt dem dit",
		"finn alle pdf-er større enn 10 MB",
		"vis de største filene her",
		"how many lines are in notes.txt",
		"what's in this folder",
		"slett loggfilene eldre enn en uke",
		"er llmhost oppe?",
		"kopier rapport.pdf til llmhost",
		"commit alt med meldingen fikset bug",
		"oppdater alle pakkene mine",
		"hvilke VM-er kjører i Azure?",
		"delete the log files older than 7 days",
		// first word is a command, but the line is prose
		"find all pdfs bigger than 10 MB",
		"sort this csv by the date column",
		"kill the process that is using port 3000",
		"list all the files in my downloads folder",
		"show me the biggest files",
		"cat the readme and tell me what it does?",
		"ping llmhost and tell me if it is up",
		"?ls",
	}
	for _, l := range lines {
		if d := Decide(l, e); !d.AI {
			t.Errorf("request not sent to AI: %q", l)
		}
	}
}
