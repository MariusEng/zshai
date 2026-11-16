#!/usr/bin/env python3
"""Requests for datagen: the teacher model writes what a user would type, per category and
language, looking at a fixture home's real file names.

    python3 datagen/gen_requests.py --server URL --model M --worlds DIR --seeds 1-8 --per 6 > requests.jsonl

Each line: {"seed", "cwd" (relative to the home), "lang", "cat", "request"}. stdlib only.
"""
import argparse, json, os, random, re, sys, urllib.request

# category -> (where the user is, what kind of requests, share of "how do I" questions)
CATS = {
    "find":      (["", "Documents", "Downloads"], "find files by name, type, size or age (largest, newest, older than, bigger than)"),
    "size":      (["", "Downloads", "projects"], "sizes of files and folders, disk space, what takes the most space"),
    "count":     (["", "data", "logs", "Pictures"], "count files, lines, words, rows, matches"),
    "list":      (["", "Pictures", "Downloads", "Documents"], "list files sorted or filtered: newest, largest, only folders, hidden files, by extension"),
    "organize":  (["Pictures", "Downloads", "Documents"], "make folders and move files into them: by type, date or name pattern"),
    "rename":    (["Pictures", "Downloads"], "rename files: lowercase extensions, add prefixes/dates, replace spaces"),
    "copy":      (["", "Documents", "projects"], "copy files and folders, back them up locally"),
    "delete":    (["Downloads", "logs", ""], "delete or clean up files: temp files, old logs, duplicates, empty folders"),
    "archive":   (["", "backup", "Documents"], "zip/tar: create archives, list what's inside, extract"),
    "search":    (["projects", "logs", "Documents"], "search text inside files: TODOs, errors, a word, in which files"),
    "text":      (["data", "logs", "Documents"], "text processing: show parts of a file, replace text, unique lines, sort lines, first/last lines"),
    "csv":       (["data"], "CSV/TSV files: show as a table, sort, filter, sum a column, count rows, pick columns"),
    "json":      (["data"], "JSON and JSON Lines files: pretty print, show as a table, pick fields, filter, count, sort, list keys"),
    "logs":      (["logs"], "log analysis: errors, count per level, last lines, lines from a time, follow"),
    "system":    ([""], "system info: OS version, uptime, CPU, memory, disk, battery, hostname, who is logged in"),
    "process":   ([""], "processes: what uses the most CPU or memory, is X running, which process listens on a port, stop a process"),
    "network":   ([""], "network: my IP address, ping a host, open ports, download a URL, DNS lookup, is the internet up"),
    "remote":    ([""], "ssh/scp/rsync/ping to the hosts in ~/.ssh/config: copy files there, disk space there, run something there"),
    "git":       (["projects/*"], "git: status, log, diff, branches, who changed what, last commit"),
    "gitchange": (["projects/*"], "git changes: commit, new branch, switch branch, stash, undo changes to a file, amend, pull, push"),
    "packages":  ([""], "install, update, remove, search or list packages and programs"),
    "media":     (["Movies", "Pictures", "Music", "Downloads"], "convert video/audio/images (needs ffmpeg/ImageMagick etc.), get video length, resize images"),
    "dates":     ([""], "dates and time: what day is it, time in another city, days until a date, week number, timestamps"),
    "perms":     (["projects/scripts", ""], "permissions and ownership: make executable, who can read, chmod"),
    "shell":     ([""], "the shell itself: history, last command, environment variables, PATH, aliases"),
    "showhow":   (["", "data", "projects/*"], "\"how do I …\" / \"hvordan …\" questions asking for a command to learn, not to run"),
    "explain":   (["", "projects/*"], "\"explain …\" / \"forklar …\" / \"what does … do\" about a concrete shell command the user writes out"),
    "ambiguous": (["", "Downloads"], "vague requests where a careful assistant should ask a short clarifying question first (e.g. 'delete the files', 'move them')"),
    "risky":     (["", "projects/*"], "dangerous things: delete everything, force push, reset hard, wipe a disk, chmod -R 777"),
}

LANG = {"en": "English", "no": "Norwegian Bokmål (casual, the way Norwegians type in a terminal)"}

def tree(home, cwd):
    out = []
    base = os.path.join(home, cwd)
    for name in sorted(os.listdir(base)):
        if name in (".git",):
            continue
        p = os.path.join(base, name)
        if os.path.isdir(p):
            kids = [k for k in sorted(os.listdir(p)) if k != ".git"][:10]
            out.append(f"{name}/ ({', '.join(kids)})")
        else:
            out.append(name)
    return "; ".join(out)

def chat(server, model, prompt):
    body = json.dumps({"model": model, "temperature": 0.9, "max_tokens": 700,
                       "messages": [{"role": "user", "content": prompt}]}).encode()
    req = urllib.request.Request(server.rstrip("/") + "/chat/completions", body, {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=180) as r:
        return json.load(r)["choices"][0]["message"]["content"]

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--server", required=True); ap.add_argument("--model", required=True)
    ap.add_argument("--worlds", required=True); ap.add_argument("--seeds", default="1-8")
    ap.add_argument("--per", type=int, default=6); ap.add_argument("--cats", default=",".join(CATS))
    a = ap.parse_args()
    lo, hi = map(int, a.seeds.split("-"))
    for seed in range(lo, hi + 1):
        home = os.path.join(a.worlds, f"w{seed}")
        R = random.Random(seed * 1000)
        for cat in a.cats.split(","):
            where, what = CATS[cat]
            for lang in ("en", "no"):
                cwd = R.choice(where)
                if cwd.endswith("/*"):
                    cwd = cwd[:-1] + R.choice(sorted(os.listdir(os.path.join(home, cwd[:-2]))))
                    if not os.path.isdir(os.path.join(home, cwd)):
                        cwd = "projects"
                if not os.path.isdir(os.path.join(home, cwd)):
                    cwd = ""
                hosts = open(os.path.join(home, ".ssh/config")).read().split()
                hosts = [hosts[i + 1] for i, w in enumerate(hosts) if w == "Host"]
                prompt = (f"You write test requests for a natural-language terminal assistant (zsh on macOS and Linux).\n"
                          f"The user is in ~/{cwd} (shown as cwd). What is there: {tree(home, cwd)}\n"
                          f"Their home has: {tree(home, '')}\nssh hosts: {', '.join(hosts)}\n\n"
                          f"Write {a.per} different requests about: {what}.\n"
                          f"Language: {LANG[lang]}. Write like a real user: mostly short, some longer, varied wording, "
                          f"some without punctuation or with a small typo. Plain language, never the shell command itself"
                          f"{' (except the command being explained)' if cat == 'explain' else ''}. Use real file and folder names from above when "
                          f"the request is about a file. Don't number them. One request per line, nothing else.")
                try:
                    txt = chat(a.server, a.model, prompt)
                except Exception as e:
                    print(f"seed {seed} {cat} {lang}: {e}", file=sys.stderr)
                    continue
                n = 0
                for line in txt.splitlines():
                    line = re.sub(r"^\s*([-*•]|\d+[.)])\s*", "", line).strip().strip('"').strip()
                    if 4 <= len(line) <= 200 and n < a.per:
                        print(json.dumps({"seed": seed, "cwd": cwd, "lang": lang, "cat": cat, "request": line}, ensure_ascii=False), flush=True)
                        n += 1

if __name__ == "__main__":
    main()
