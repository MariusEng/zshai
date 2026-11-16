#!/usr/bin/env python3
"""Run requests through zshai with the teacher model and keep what works.

For each request: `zshai ask` in a fixture home (HOME = the fixture, isolated config/log),
with the teacher model and all of zshai's checks and repair rounds. The first prompt zshai
sent (context + request + mode) and the final, checked answer become one training example.
Answers are then executed:
  - read commands in the fixture itself;
  - change/delete commands in a throwaway copy of the fixture;
  - remote, network, package, sudo, kill and GUI commands are not executed (kept, marked).
Anything that fails is dropped.

    python3 datagen/run_teacher.py --zshai ./zshai --worlds DIR --requests req.jsonl \
        --server URL --model M --out examples.jsonl [--stock 0.3]

Resumable: requests already in --out (same seed/cwd/request) are skipped. stdlib only.
"""
import argparse, json, os, random, re, shlex, shutil, sqlite3, subprocess, sys, tempfile, time

OPTIONAL = ["jq", "rg", "fd", "fdfind", "ffmpeg", "ffprobe", "magick", "convert", "tree", "bat", "batcat", "htop",
            "7z", "7zz", "pandoc", "yq", "fzf", "eza", "exa", "gh", "pdftotext", "exiftool", "sqlite3", "wget", "tldr"]
NOEXEC = re.compile(r"(^|[|;&(]\s*|\bsudo\s+)(sudo|ssh|scp|rsync|ping|curl|wget|dig|nslookup|host|traceroute|nc|telnet|open|xdg-open|"
                    r"less|more|man|vim|vi|nano|top|htop|watch|kill|pkill|killall|brew|apt|apt-get|pacman|dnf|shutdown|reboot|"
                    r"launchctl|systemctl|osascript|say|afplay|pbcopy|pbpaste|networksetup|softwareupdate|diskutil|mount|umount|"
                    r"caffeinate|screencapture|npm|npx|pip|pip3|python3?\s+-m\s+pip|az|gh|docker|podman)\b")
GITNET = re.compile(r"\bgit\s+(push|pull|fetch|clone|remote\s+update)\b")
FOLLOW = re.compile(r"\btail\s+(-\S*f|--follow)|\bjournalctl\b.*\s-f\b")
ABSPATH = re.compile(r"(?<![\w~$.])/(?!dev/null\b)(?:[A-Za-z]|$)")
ERRPAT = re.compile(r"(?im)^usage:|illegal option|invalid option|unrecognized option|unknown option|syntax error|"
                    r"parse error|command not found|no such file or directory|^\S+: error|^error:|jq: error|bad substitution")

def stock_path(base_path, cache):
    """A PATH with the optional tools hidden: a dir of symlinks to everything else."""
    if "stock" in cache:
        return cache["stock"]
    d = tempfile.mkdtemp(prefix="zshai-stock-")
    for p in base_path.split(":"):
        if not os.path.isdir(p):
            continue
        for name in os.listdir(p):
            dst = os.path.join(d, name)
            if name in OPTIONAL or os.path.lexists(dst):
                continue
            src = os.path.join(p, name)
            if os.access(src, os.X_OK) and not os.path.isdir(src):
                os.symlink(src, dst)
    cache["stock"] = d
    return d

def parse_emit(out):
    m = re.search(r"zshai_r=\((.*)\)\s*$", out, re.S)
    if not m:
        return {}
    toks = shlex.split(m.group(1))
    return dict(zip(toks[0::2], toks[1::2]))

def parse_raw(raw):
    raw = re.sub(r"(?s)<think>.*?</think>", "", raw).replace("\r\n", "\n")
    hdr, body, seen = {}, [], False
    lines = raw.split("\n")
    i = 0
    for i, l in enumerate(lines):
        t = l.strip()
        if t == "---":
            seen = True
            break
        k, _, v = t.partition(":")
        if k in ("r", "m", "a", "n", "x", "e") and _:
            hdr.setdefault(k, []).append(v.strip())
    if seen:
        for l in lines[i + 1:]:
            t = l.strip()
            if t == "---" or re.match(r"^(r|m|a|n|x): ", t) or t.startswith("e:"):
                break
            body.append(l)
    return hdr, "\n".join(body).strip().strip("`").strip()

EN_WORDS = re.compile(r"\b(the|and|with|files|lists|shows|which|this|from)\b", re.I)
NO_WORDS = re.compile(r"\b(og|filene|som|med|er|viser|mappen|fra|til|den|det)\b", re.I)

def wrong_language(lang, explain):
    t = " ".join(explain)
    if not t:
        return False
    en, no = len(EN_WORDS.findall(t)), len(NO_WORDS.findall(t))
    return (lang == "no" and en > no) or (lang == "en" and no > en + 1)

def judge_vague(server, model, q, prompt):
    """Vague requests: should zshai ask first? Returns the question, or "" to go ahead."""
    import urllib.request
    lang = "Norwegian Bokmål" if q["lang"] == "no" else "English"
    ask = (f"A terminal assistant got this request, with this context:\n{prompt}\n"
           "Would a careful assistant need to ask a short clarifying question before writing a command, because the "
           "request doesn't say WHICH files/folders/target it means (e.g. 'delete them', 'move the files', 'clean up')? "
           f"If yes, reply exactly 'ASK: <one short question in {lang}>'. If the request is clear enough, reply exactly 'OK'.")
    body = json.dumps({"model": model, "temperature": 0, "max_tokens": 80, "messages": [{"role": "user", "content": ask}]}).encode()
    req = urllib.request.Request(server.rstrip("/") + "/chat/completions", body, {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        t = json.load(r)["choices"][0]["message"]["content"].strip()
    return t[4:].strip() if t.upper().startswith("ASK:") else ""

def answer_text(r, m, a="", n="", explain=(), cmd=""):
    s = f"r: {r}\nm: {m}\na: {a}\nn: {n}\nx: 0\n"
    for e in explain:
        s += f"e: {e}\n"
    return s + "---\n" + cmd

def tree_digest(root):
    """What a change did, comparable between two runs: every path with its size."""
    import hashlib
    h = hashlib.sha1()
    for d, dirs, files in sorted(os.walk(root)):
        dirs[:] = sorted(x for x in dirs if x != ".git")
        for f in sorted(files):
            p = os.path.join(d, f)
            try:
                h.update(f"{os.path.relpath(p, root)} {os.lstat(p).st_size}\n".encode())
            except OSError:
                pass
        for x in dirs:
            h.update(f"{os.path.relpath(os.path.join(d, x), root)}/\n".encode())
    return h.hexdigest()[:16]

def run(cmd, cwd, home, path, timeout=10):
    env = {"HOME": home, "PATH": path, "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8", "TERM": "dumb", "TZ": "Europe/Oslo"}
    try:
        p = subprocess.run(["zsh", "-f", "-c", cmd], cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                           capture_output=True, timeout=timeout)
        return p.returncode, p.stdout.decode(errors="replace"), p.stderr.decode(errors="replace")
    except subprocess.TimeoutExpired:
        return 124, "", "timeout"

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--zshai", required=True); ap.add_argument("--worlds", required=True)
    ap.add_argument("--requests", required=True); ap.add_argument("--out", required=True)
    ap.add_argument("--server", required=True); ap.add_argument("--model", required=True)
    ap.add_argument("--stock", type=float, default=0.3, help="share of requests run with optional tools hidden")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--no-judge", action="store_true", help="evaluating a model: don't ask the teacher whether vague requests need a question")
    a = ap.parse_args()
    zshai = os.path.abspath(a.zshai)
    work = tempfile.mkdtemp(prefix="zshai-dg-")
    cfgd, state, cache = (os.path.join(work, x) for x in ("cfg", "state", "cache"))
    os.makedirs(cfgd)
    base_path = os.environ["PATH"]
    pcache = {}
    done = set()
    if os.path.exists(a.out):
        for l in open(a.out):
            j = json.loads(l)
            done.add((j["seed"], j["cwd"], j["request"]))
    out = open(a.out, "a")
    reqs = [json.loads(l) for l in open(a.requests)]
    n = kept = 0
    for q in reqs:
        key = (q["seed"], q["cwd"], q["request"])
        if key in done:
            continue
        if a.limit and n >= a.limit:
            break
        n += 1
        R = random.Random(hash(key) & 0xffffffff)
        home = os.path.abspath(os.path.join(a.worlds, f"w{q['seed']}"))
        cwd = os.path.join(home, q["cwd"])
        stock = R.random() < a.stock
        path = stock_path(base_path, pcache) if stock else base_path
        json.dump({"language": q["lang"], "server": a.server, "model": a.model, "timeoutSeconds": 120,
                   "suggestInstall": "ask", "fixAttempts": 1}, open(os.path.join(cfgd, "config.json"), "w"))
        env = {**os.environ, "HOME": home, "PATH": path, "ZSHAI_CONFIG_DIR": cfgd, "ZSHAI_STATE_DIR": state,
               "ZSHAI_CACHE_DIR": cache, "ZSH_VERSION": "5.9"}
        t0 = time.time()
        p = subprocess.run([zshai, "ask", "--cwd", cwd, "--", q["request"]], env=env, capture_output=True, text=True, timeout=600)
        ask_s = time.time() - t0
        r = parse_emit(p.stdout)
        st = r.get("status", "")
        db = sqlite3.connect(os.path.join(state, "zshai.db"))
        row = db.execute("SELECT id, outcome FROM turns ORDER BY id DESC LIMIT 1").fetchone()
        calls = db.execute("SELECT prompt, response FROM responses WHERE turn_id=? ORDER BY seq", (row[0],)).fetchall() if row else []
        db.close()
        rec = {**q, "os": sys.platform, "stock": stock, "status": st, "calls": len(calls)}
        if not calls or st in ("", "error"):
            rec["drop"] = f"status {st}: {r.get('msg', p.stderr.strip()[:200])}"
            print(json.dumps(rec, ensure_ascii=False), file=sys.stderr, flush=True)
            continue
        prompt = calls[0][0]
        hdr, body = parse_raw(calls[-1][1])
        verified, target = "", None
        question = judge_vague(a.server, a.model, q, prompt) if q["cat"] == "ambiguous" and st != "show" and not a.no_judge else ""
        if question:
            target = answer_text("read", "run", a=question)
            verified = "ask-judged"
        elif st in ("run", "confirm"):
            cmd, level = r["cmd"], r["level"]
            explain = []
            if NOEXEC.search(cmd) or GITNET.search(cmd) or FOLLOW.search(cmd) or level == "danger":
                verified = "not-run"
            elif level == "read":
                code, so, se = run(cmd, cwd, home, path)
                if code != 0 or ERRPAT.search(se) or (not so.strip() and not se.strip() and q["cat"] not in ("search", "find", "remote")):
                    rec["drop"] = f"read exit {code}: {se.strip()[:200]}"
                rec["out"] = so[:4000]
                verified = "ran"
            elif ABSPATH.search(cmd.replace(home, "~")):
                verified = "not-run"
            else:
                tmp = tempfile.mkdtemp(prefix="zshai-copy-")
                copy = os.path.join(tmp, "home")
                shutil.copytree(home, copy, symlinks=True)
                code, so, se = run(cmd.replace(home, copy), os.path.join(copy, q["cwd"]), copy, path, timeout=20)
                rec["tree"] = tree_digest(copy)
                shutil.rmtree(tmp, ignore_errors=True)
                if code != 0 or ERRPAT.search(se):
                    rec["drop"] = f"change exit {code}: {se.strip()[:200]}"
                verified = "ran-copy"
            target = answer_text(level if level != "install" else "install", "run", cmd=cmd)
        elif st == "show":
            target = answer_text(r.get("level", (hdr.get("r") or ["read"])[0]), "show", explain=r.get("explain", "").split("\n") if r.get("explain") else [], cmd=r["cmd"])
            verified = "show"
            if wrong_language(q["lang"], r.get("explain", "").split("\n")):
                rec["drop"] = "explanation in the wrong language"
        elif st == "answer" and r.get("followup"):
            target = answer_text((hdr.get("r") or ["read"])[0], "run", a=r["msg"])
            verified = "ask"
        elif st in ("install", "answer"):
            need = " ".join((hdr.get("n") or [""])[0].replace(",", " ").split())
            if not need and st == "install":
                need = r["cmd"].split()[-1]
            if not need:
                rec["drop"] = f"status {st} without a tool"
            target = answer_text((hdr.get("r") or ["change"])[0], "run", n=need)
            verified = "needs"
        else:
            rec["drop"] = f"status {st}"
        rec.update({"verified": verified, "seconds": round(ask_s, 2), "prompt": prompt, "target": target, "level": r.get("level", "")})
        if rec.get("drop") or target is None:
            print(json.dumps(rec, ensure_ascii=False), file=sys.stderr, flush=True)
            continue
        out.write(json.dumps(rec, ensure_ascii=False) + "\n"); out.flush()
        kept += 1
    print(f"done: {n} requests, {kept} kept", file=sys.stderr)

if __name__ == "__main__":
    main()
