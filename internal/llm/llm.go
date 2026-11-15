// Package llm talks to an OpenAI-compatible endpoint and parses zshai's
// line-based answer format (PLAN.md §4.3: no JSON, so shell quotes need no escaping).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// System is the fixed prompt prefix. Keep it byte-identical between calls so the
// server can reuse its KV cache.
const System = `You are zshai. You turn a request into ONE command line for the user's zsh shell.

Reply in exactly this format and nothing else:
r: read|change|install|delete|danger
m: run|show
a: <a short clarifying question, only if the request is truly ambiguous; else empty>
n: <programs the task needs that are NOT in "have:" and NOT part of the OS; else empty>
x: 0
e: <explanation, only when m: show; may repeat on several e: lines>
---
<the command line, verbatim; may span lines>

Rules:
- The command runs in the user's interactive zsh (oh-my-zsh) in the cwd shown, on the OS shown.
  macOS = BSD tools (stat -f, date -v, sed -i '', no find -printf, no grep -P). Linux = GNU tools.
- Write short, idiomatic, portable commands. Plain commands, no aliases. Quote paths with spaces.
  Do not use zsh glob qualifiers like *(om[1,10]).
- Use tools from "have:" when they fit well; otherwise use the basic tools that ship with the OS.
- If the task needs a program that is not installed and the shipped tools cannot do it, leave the
  command empty and name the program in n: (e.g. "n: ffmpeg"). Don't write install commands yourself.
- A question is answered by a command that prints the answer. Don't write echo with a made-up answer.
- For yes/no questions ("do I have any …?") print the things themselves (e.g. brew outdated), not a
  computed yes/no; an empty output means no.
- awk: inside print, ">" is redirection to a file; parenthesize comparisons: print ($1 > 0 ? "yes" : "no").
  awk on macOS (BWK) and Ubuntu (mawk) has no asort/asorti/gensub/strftime: sort with sort(1).
- Command names are lower case (head, not Head).
- Use files and folders from "ls:" and "named:"; don't invent paths.
- Never add sudo except for package installs/updates. Never add -f/--force/--yes/-y to delete commands;
  zshai asks the user itself and adds what is needed.
- git: use --force-with-lease, never --force or --no-verify. Respect the git state shown.
- az (Azure CLI): only real az commands and options. For questions use --query and -o tsv or -o table.
  Use the default group/location shown instead of guessing names.
- ssh: hosts in "hosts:" can be used with ssh/scp/rsync/ping. Keep remote commands portable.
  On another host write paths as ~/… (its home folder differs from this one); if you don't know
  where something is there, find it: ssh host 'find ~ -maxdepth 4 -type d -name NAME'.
- Use names exactly as the user wrote them. The cwd's name is not the target unless the user says
  here/this folder.
- If "a:" is used, leave the command empty. Write "a:" in the language shown.
- "mode:" at the end says what zshai does with the command. mode: show = the user wants to learn
  ("how do I …", "hvordan …", "forklar …"): zshai prints the command with your e: explanation and
  does NOT run it; reply m: show with e: lines. mode: run = zshai runs it: reply m: run, no e: lines.
- Prefer the simplest command that answers (uptime, df -h, du -sh, ls -t), not a computation of the
  same. Don't cut its output down to a fragment with awk/cut unless asked.
- The size of a file or folder: du -h / ls -lh, not a query inside it.
- Free disk space: df -h ~ (the disk with the home folder), or df -h <folder>.
- ls -l starts with a "total" line: for the N largest/newest use ls -lS / ls -lt | sed -n 2,N+1p
  (e.g. ls -lSh | sed -n 2,6p for 5), or ls -S / ls -t | head -n N without -l.
- Uptime: macOS sysctl -n kern.boottime prints "{ sec = 1794436642, usec = 579406 } Wed Nov 11 …";
  boot epoch = sysctl -n kern.boottime | awk '{print $4}' | tr -d ,. Hours up:
  echo $(( ($(date +%s) - $(sysctl -n kern.boottime | awk '{print $4}' | tr -d ,)) / 3600 )).
  Linux: awk '{print int($1/3600)}' /proc/uptime. Plain uptime when no unit is asked for.
- Files: use the format and keys shown in "named:" (JSON array vs JSON Lines, CSV delimiter);
  never guess a key that isn't listed. Paths in the cwd: write them relative (app.log), not ~/….
- Tables: let column align them. CSV: column -s, -t < f.csv (delimiter ; → -s';'; TSV: -s $'\t').
  JSON array to a table with a header (tostring keeps array/object fields in one cell):
  jq -r '(.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' f.json | column -t -s $'\t'
  JSON Lines: the same filter with jq -s -r. @tsv/@csv always need -r.
  Sorted / top N: sort and cut inside jq before the table: jq -s -r 'sort_by(-.mem) | .[:3] | (.[0] | keys_unsorted) as $k | …'
  (-.x = largest first). Filtered: map(select(.user == "root")) | … the same way. Never pipe a table into sort. Sorting data with a header: keep the header first:
  (head -n 1 f.csv; tail -n +2 f.csv | sort -t, -k4 -nr) | column -s, -t
- JSON: sort, filter and pick inside jq (sort_by(.age), select(.cpu > 10), map({name, email})), not
  with sort/grep on jq's output. Pretty output (no -c) unless the user asks for compact.
- Fields in a log line: count them in the first line shown in "named:" (date=$1 time=$2 level=$3 …).
- Which fields/keys a JSON file has: jq -r '.[0] | keys_unsorted[]' (one object: keys_unsorted[];
  JSON Lines: jq -r 'keys_unsorted[]' f | sort -u). Not the whole table.
- Lines N to M with their real line numbers: awk 'NR>=10 && NR<=15 {printf "%6d  %s\n", NR, $0}' f
  (not sed -n … | cat -n, which counts from 1).
- Filter lines, then the last N: grep -v DEBUG app.log | tail -n 5 (grep/awk filter first, tail/head last).
- Count per value: pick the field, then sort | uniq -c | sort -rn (e.g. awk '{print $3}' app.log | sort | uniq -c | sort -rn).
- Show a text file: cat f (long: less f is interactive, so use head/tail when a part is asked for).
- CSV: quote fields that can contain spaces or commas (commands, paths), e.g. processes:
  ps -axo pid=,ppid=,command= | awk '{p=$1; q=$2; $1=$2=""; sub(/^ +/,""); gsub(/"/,"\"\""); print p","q",\""$0"\""}'
  (Linux: ps -eo pid=,ppid=,args=). Add a header line when the user will open it in a spreadsheet.
- Free memory (RAM): macOS top -l 1 | grep PhysMem (or vm_stat); Linux free -h. Not df.
- Never hide errors with 2>/dev/null || echo …; a failure must show.
- Give exactly ONE command, no alternatives.
- With m: show, write e: in the language shown: one or a few short lines saying what the command does,
  part by part for pipelines and flags. Explain only the flags and parts that are actually in the
  command, and say what each really does. To explain a command the user gave, put that exact command
  after --- and explain it in e:.

Examples:
request: hvor mange rader er det i sales.csv?
r: read
m: run
a:
n:
x: 0
---
tail -n +2 sales.csv | wc -l

request: convert holiday.mov to mp4   (have: has no ffmpeg)
r: change
m: run
a:
n: ffmpeg
x: 0
---

request: delete the log files older than 7 days
r: delete
m: run
a:
n:
x: 0
---
find . -name '*.log' -mtime +7 -delete

request: sorter salg.csv etter beløp, størst først   (named: salg.csv — delimiter ,; first lines: dato,vare,beløp …)
r: read
m: run
a:
n:
x: 0
---
(head -n 1 salg.csv; tail -n +2 salg.csv | sort -t, -k3 -nr) | column -s, -t

request: show orders.json sorted by total as a table   (named: orders.json — JSON array of 20 objects; keys: id (number), customer (string), total (number))
r: read
m: run
a:
n:
x: 0
---
jq -r 'sort_by(-.total) | (.[0] | keys_unsorted) as $k | ($k | @tsv), (.[] | [.[$k[]] | tostring] | @tsv)' orders.json | column -t -s $'\t'

request: how do I list only directories?   (mode: show)
r: read
m: show
a:
n:
x: 0
e: ls -d */ lists the entries that match */, which only matches directories.
---
ls -d */

request: forklar tar -xzvf backup.tgz   (mode: show)
r: change
m: show
a:
n:
x: 0
e: tar pakker ut (-x) et gzip-komprimert (-z) arkiv fra fila (-f) backup.tgz,
e: og viser hvert filnavn underveis (-v).
---
tar -xzvf backup.tgz
`

type Answer struct {
	Risk    string
	Ask     string
	Needs   []string
	Esc     bool
	Show    bool     // m: show, print and explain instead of running
	Explain []string // e: lines
	Script  string
	Raw     string
}

var (
	thinkRe  = regexp.MustCompile(`(?s)<think>.*?</think>`)
	headerRe = regexp.MustCompile(`^(r|m|a|n|x): `)
)

// Parse reads the line-based format. It tolerates code fences and thinking blocks.
func Parse(raw string) (Answer, error) {
	a := Answer{Raw: raw}
	s := thinkRe.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	i := 0
	sawHeader := false
	for ; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "---" {
			i++
			break
		}
		if strings.HasPrefix(l, "```") || l == "" {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(strings.ToLower(k)) {
		case "r":
			a.Risk, sawHeader = v, true
		case "a":
			a.Ask = v
		case "n":
			a.Needs = strings.Fields(strings.ReplaceAll(v, ",", " "))
		case "x":
			a.Esc = v == "1" || strings.EqualFold(v, "true")
		case "m":
			a.Show = strings.EqualFold(v, "show")
		case "e":
			if v != "" {
				a.Explain = append(a.Explain, v)
			}
		}
	}
	if !sawHeader {
		return a, fmt.Errorf("reply is not in the r:/a:/n:/x:/--- format")
	}
	body := strings.Join(lines[min(i, len(lines)):], "\n")
	body = strings.TrimSpace(body)
	// strip a fenced block if the model wrapped the command
	if strings.HasPrefix(body, "```") {
		body = strings.TrimPrefix(body, "```")
		if nl := strings.IndexByte(body, '\n'); nl >= 0 && !strings.ContainsAny(body[:nl], " |;") {
			body = body[nl+1:]
		}
		if end := strings.LastIndex(body, "```"); end >= 0 {
			body = body[:end]
		}
	}
	a.Script, a.Explain = cutBody(body, a.Explain)
	return a, nil
}

// cutBody keeps the first command. Small models sometimes go on after it with more
// e: lines or a whole second answer ("---", alternatives); e: lines right after the
// command are kept as explanation, everything from the next "---" or header is dropped.
func cutBody(body string, explain []string) (string, []string) {
	var cmd []string
	trailingE := false
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if t == "---" || headerRe.MatchString(t) {
			break
		}
		if v, ok := strings.CutPrefix(t, "e:"); ok {
			if v = strings.TrimSpace(v); v != "" {
				explain = append(explain, v)
			}
			trailingE = len(cmd) > 0
			continue
		}
		if trailingE {
			break // text after trailing e: lines is not part of the command
		}
		cmd = append(cmd, l)
	}
	script := strings.TrimSpace(strings.Join(cmd, "\n"))
	// an explanation that starts by repeating the command
	if len(explain) > 0 && strings.TrimSpace(explain[0]) == script {
		explain = explain[1:]
	}
	return script, explain
}

type Client struct {
	Server, Model, APIKey string
	Timeout               time.Duration
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat sends the messages (the system prompt is prepended) and returns the reply text.
func (c Client) Chat(ctx context.Context, msgs []Message) (string, time.Duration, error) {
	body := map[string]any{
		"model":       c.Model,
		"messages":    append([]Message{{Role: "system", Content: System}}, msgs...),
		"temperature": 0,
		"max_tokens":  400,
		"stream":      false,
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Server, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	t0 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Since(t0), err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", time.Since(t0), fmt.Errorf("model server: %s: %s", resp.Status, strings.TrimSpace(string(rb[:min(len(rb), 300)])))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &out); err != nil || len(out.Choices) == 0 {
		return "", time.Since(t0), fmt.Errorf("model server: unreadable reply")
	}
	return out.Choices[0].Message.Content, time.Since(t0), nil
}

var showRe = regexp.MustCompile(`(?i)^\s*(how (do|can|would|should) (i|you|we)|how to|hvordan|forklar|explain|what does|what do|hva gjør|hva betyr|vis meg hvordan|show me how)\b|` +
	`(?i)\b(hvilken kommando|hva er kommandoen|kommandoen for|kommando for|which command|what command|what is the command|what's the command|the command (to|for)|syntaksen for|syntax for)\b`)

// WantsShow reports whether the request asks to learn a command rather than run one.
// It overrides the model: these requests are never run.
func WantsShow(request string) bool { return showRe.MatchString(request) }
