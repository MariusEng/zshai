"""End-to-end terminal tests for the zshai plugin (offline, deterministic).

Drives a real interactive zsh (oh-my-zsh + robbyrussell + git + zshai) in a pty,
with a fake model server that returns canned answers, and checks the rendered
screen and the files on disk. The safety scenarios are the point: nothing that
needs confirmation may run without it, even when zshai itself fails.

    PYTHONPATH=<dir with pyte> python3 tests/pty/run.py [scenario ...]

Needs: zsh, oh-my-zsh in ~/.oh-my-zsh, the built ./zshai, pyte (pip install pyte).
"""
import fcntl
import http.server
import json
import os
import pty
import select
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

import pyte

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
COLS, ROWS = 120, 40

# request substring -> model reply (zshai's line format, PLAN.md §4.3)
CANNED = {
    # an answer to a clarifying question arrives with the earlier request attached
    "earlier request: \"rydd opp\"": "r: read\na:\nn:\nx: 0\n---\nls -la logs",
    "tenk lenge": "r: read\nm: run\na:\nn:\nx: 0\n---\nprint ferdig",
    "ordrene som tabell": "r: read\nm: run\na:\nn:\nx: 0\n---\njq -r '(.[0] | keys_unsorted) as $k | ($k | @tsv), "
                          "(.[] | [.[$k[]] | tostring] | @tsv)' orders.json | column -t -s $'\\t'",
    "hvor mange rader": "r: read\na:\nn:\nx: 0\n---\ntail -n +2 sales.csv | wc -l",
    "slett loggfilene": "r: delete\na:\nn:\nx: 0\n---\nfind ./logs -name '*.log' -mtime +7 -delete",
    "slett alle loggfilene": "r: delete\na:\nn:\nx: 0\n---\nrm logs/*.log",
    "flytt sales.csv": "r: delete\na:\nn:\nx: 0\n---\nmv sales.csv Pictures/",
    "gjør om ferie.mov": "r: change\na:\nn: ffmpeg\nx: 0\n---\n",
    "forkast alle endringene": "r: danger\na:\nn:\nx: 0\n---\ngit checkout -- .",
    "rydd opp": "r: read\na: Hva vil du rydde opp i: loggfilene eller bildene?\nn:\nx: 0\n---\n",
    "slett hjemmemappa": "r: danger\na:\nn:\nx: 0\n---\nrm -rf ~",
    # the model says run, but "hvordan" must still only show
    "hvordan sletter jeg loggfilene": "r: delete\nm: run\na:\nn:\nx: 0\n---\nrm logs/*.log",
    "forklar tar": "r: change\nm: show\na:\nn:\nx: 0\ne: tar pakker ut (-x) et gzip-arkiv (-z) fra fila (-f) a.tgz,\ne: og viser hvert filnavn (-v).\n---\ntar -xzvf a.tgz",
}


# replies to a fix request (zshai sends the failed command and its error output)
FIX_CANNED = {
    "regn ut oppetiden": "r: read\nm: run\na:\nn:\nx: 0\n---\nprint 52",
    "tell linjene i salg.csv": "r: read\nm: run\na:\nn:\nx: 0\n---\nwc -l sales.csv",
    "vis fila notat.txt": "r: read\nm: run\na:\nn:\nx: 0\n---\ncat notat.md",
}
CANNED.update({
    "regn ut oppetiden": "r: read\nm: run\na:\nn:\nx: 0\n---\nprint -- -498524; print -u2 'date: illegal time format'",
    "hent nettsiden": "r: read\nm: run\na:\nn:\nx: 0\n---\nprint -u2 'Receiving objects: 100%'; print ferdig",
    "tell linjene i salg.csv": "r: read\nm: run\na:\nn:\nx: 0\n---\nwc -l salg.csv",
    "finn ordet zebra": "r: read\nm: run\na:\nn:\nx: 0\n---\ngrep zebra sales.csv",
    "vis fila notat.txt": "r: read\nm: run\na:\nn:\nx: 0\n---\ncat notat.txt",
})


SYSTEMS = []  # the system prompts the fake model got


class FakeModel(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        SYSTEMS.append(body["messages"][0]["content"])
        user = [m["content"] for m in body["messages"] if m["role"] == "user"][0]
        req = [l for l in user.splitlines() if l.startswith("request: ")][0][len("request: "):]
        table = FIX_CANNED if "failed command:" in user else CANNED
        reply = next((v for k, v in table.items() if k in req), "r: read\na: ?\nn:\nx: 0\n---\n")
        # slow answers, so the spinner shows: the first try fails zshai's checks, the retry works
        if "tenk lenge" in req:
            time.sleep(1.2)
        if "prøv igjen" in req:
            time.sleep(1.0)
            tries = sum(1 for m in body["messages"] if m["role"] == "user")
            reply = "r: read\nm: run\na:\nn:\nx: 0\n---\n" + ("nosuchcmd_xyz" if tries == 1 else "print ferdig")
        out = json.dumps({"choices": [{"message": {"role": "assistant", "content": reply}}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *a):
        pass


def fixtures(work):
    os.makedirs(f"{work}/logs")
    os.makedirs(f"{work}/Pictures")
    for i, days in ((1, 11), (2, 13), (3, 0)):
        p = f"{work}/logs/app{i}.log"
        with open(p, "wb") as f:
            f.write(os.urandom(i * 40000))
        t = time.time() - days * 86400
        os.utime(p, (t, t))
    with open(f"{work}/orders.json", "w") as f:
        f.write('[{"id": 1, "customer": "Kari", "total": 250.5}, {"id": 12, "customer": "Ola Nordmann", "total": 9}]\n')
    with open(f"{work}/sales.csv", "w") as f:
        f.write("date,item,amount\n2026-11-01,a,10\n2026-11-02,b,20\n2026-11-03,c,30\n")
    repo = f"{work}/repo"
    os.makedirs(repo)
    g = lambda *a: subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", *a], cwd=repo, check=True,
                                  capture_output=True)
    g("init", "-q", "-b", "main")
    open(f"{repo}/a.txt", "w").write("one\ntwo\n")
    g("add", ".")
    g("commit", "-qm", "init")
    open(f"{repo}/a.txt", "w").write("one\nTWO changed\n")


ROWS_SEEN = []  # the cursor's row after each step of the current scenario
COLOR_PROBES = ["tar pakker ut"]  # text whose colour the scenarios check
COLOR_OF = {}
KEY_COLOURS = {}  # colour of the first letter of ja / nei / vis / endre on the question line


def colour(word, want):
    got = COLOR_OF.get(word)
    return [] if got == want else [f"{word!r} has colour {got}, want {want}"]


def drive(work, steps, port, extra_env=None):
    ROWS_SEEN.clear()
    zdot = f"{work}/.zdot"
    os.makedirs(zdot, exist_ok=True)
    with open(f"{zdot}/.zshrc", "w") as f:
        f.write(f"""export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="robbyrussell"
DISABLE_AUTO_UPDATE=true
zstyle ':omz:update' mode disabled
plugins=(git)
source $ZSH/oh-my-zsh.sh
HISTFILE={zdot}/history
source {REPO}/plugin/zshai.plugin.zsh
""")
    env = dict(os.environ)
    env.update({
        "ZDOTDIR": zdot, "TERM": "xterm-256color", "PATH": REPO + ":" + env["PATH"],
        "ZSHAI_CONFIG_DIR": f"{work}/.cfg", "ZSHAI_STATE_DIR": f"{work}/.state", "ZSHAI_CACHE_DIR": f"{work}/.cache",
        "XDG_DATA_HOME": f"{work}/.xdg", "ZSHAI_TRASH_DIR": f"{work}/.xdg/Trash", "ZSHAI_SERVER": f"http://127.0.0.1:{port}/v1", "ZSHAI_MODEL": "fake",
        "ZSHAI_BG": "dark",  # no OSC 11 wait; the bg-detection scenarios unset it
    })
    env.pop("COLORFGBG", None)
    extra_env = dict(extra_env or {})
    cfg = {"language": "no", **extra_env.pop("_cfg", {})}
    bg_reply = extra_env.pop("_bg_reply", None)  # how the fake terminal answers OSC 11
    env.update(extra_env)
    os.makedirs(f"{work}/.cfg", exist_ok=True)
    json.dump(cfg, open(f"{work}/.cfg/config.json", "w"))
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(work)
        os.execvpe("zsh", ["zsh", "-i"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    screen = pyte.Screen(COLS, ROWS)
    stream = pyte.ByteStream(screen)

    def pump(t):
        end = time.time() + t
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], 0.03)
            if r:
                try:
                    data = os.read(fd, 65536)
                except OSError:
                    return
                if bg_reply and b"\x1b]11;?" in data:
                    os.write(fd, bg_reply)
                stream.feed(data)

    pump(2.0)
    arrows = []
    for keys, wait in steps:
        os.write(fd, keys.encode())
        pump(wait)
        row = screen.buffer[screen.cursor.y]
        arrows.append(next((row[x].fg for x in range(COLS) if row[x].data == "➜"), None))
        ROWS_SEEN.append("".join(row[x].data for x in range(COLS)).rstrip())
    KEY_COLOURS.clear()
    for y in range(ROWS):
        line = "".join(screen.buffer[y][x].data for x in range(COLS))
        i = line.find("ja / nei / vis / endre")
        if i >= 0:
            for word in ("ja", "nei", "vis", "endre"):
                x = line.find(word, i)
                KEY_COLOURS[word[0]] = screen.buffer[y][x].fg
    COLOR_OF.clear()
    for y in range(ROWS):
        line = "".join(screen.buffer[y][x].data for x in range(COLS))
        for word in COLOR_PROBES:
            i = line.find(word)
            if i >= 0:
                COLOR_OF[word] = screen.buffer[y][i].fg
    os.write(fd, b"\x15exit\r")   # Ctrl-U first: never run a half-edited line
    pump(0.3)
    text = "\n".join(l.rstrip() for l in screen.display).rstrip()
    return text, arrows


# name -> (steps, extra env, checks(work, screen, arrows) -> list of failures)
def exists(work, *ps):
    return [f"missing {p}" for p in ps if not os.path.exists(f"{work}/{p}")]


def gone(work, *ps):
    return [f"still there: {p}" for p in ps if os.path.exists(f"{work}/{p}")]


def has(screen, *ss):
    """Substrings of the screen; "\n3" also matches a line "   3" (BSD wc pads numbers)."""
    lines = {l.strip() for l in screen.split("\n")}
    def found(s):
        return s in screen or (s.startswith("\n") and s.strip() in lines)
    return [f"screen lacks {s!r}" for s in ss if not found(s)]


def db(work, sql):
    """Rows from the SQLite log (internal/store)."""
    import sqlite3
    path = f"{work}/.state/zshai.db"
    if not os.path.exists(path):
        return []
    time.sleep(0.3)  # background `zshai outcome/done` writes
    with sqlite3.connect(path) as c:
        return c.execute(sql).fetchall()


def db_is(work, sql, want):
    got = db(work, sql)
    return [] if got == want else [f"log: {sql} -> {got}, want {want}"]


def trash_has(work, *names):
    files = os.listdir(f"{work}/.xdg/Trash/files") if os.path.isdir(f"{work}/.xdg/Trash/files") else []
    return [f"not in trash: {n}" for n in names if n not in files]


SCENARIOS = {
    "read-runs": (
        [("zshai: hvor mange rader er det i sales.csv?\r", 2)], None,
        lambda w, s, a: has(s, "hvor mange rader er det i sales.csv?", "tail -n +2 sales.csv | wc -l", "\n3")
        + db_is(w, "select request, outcome, exit_code, mode from turns", [("hvor mange rader er det i sales.csv?", "ran", 0, "run")])
        + db_is(w, "select count(*) from responses where system_prompt like 'You are zshai%'", [(1,)])),
    "delete-asks-then-trashes": (
        [("zshai: slett loggfilene eldre enn en uke\r", 2), ("j", 1.5)], None,
        lambda w, s, a: has(s, "Skal jeg slette 2 filer", "i logs?", "papirkurven", "La 2 filer")
        + gone(w, "logs/app1.log", "logs/app2.log") + exists(w, "logs/app3.log") + trash_has(w, "app1.log", "app2.log")
        + db_is(w, "select outcome, deleted from turns", [("trashed", 1)])
        + db_is(w, "select action, via from effects order by path", [("deleted", "trash"), ("deleted", "trash")])),
    "delete-then-undo": (
        [("zshai: slett loggfilene eldre enn en uke\r", 2), ("j", 1.5), ("zshai: angre\r", 1.5)], None,
        lambda w, s, a: has(s, "La tilbake 2 av 2") + exists(w, "logs/app1.log", "logs/app2.log", "logs/app3.log")
        + db_is(w, "select action, count(*) from effects group by action order by action", [("deleted", 2), ("restored", 2)])),
    "key-letters-coloured": (
        [("zshai: flytt sales.csv til Pictures\r", 2), ("n", 1)], None,
        lambda w, s, a: [f"key {k}: {got}, want {want}" for k, want in (("j", "87d787"), ("n", "ff5f5f"), ("v", "ffd700"), ("e", "87d7ff"))
                         if (got := KEY_COLOURS.get(k)) != want]),
    "cancel-keeps-files-and-arrow": (
        [("false\r", 0.5), ("zshai: slett alle loggfilene\r", 2), ("n", 1)], None,
        lambda w, s, a: has(s, "Avbrutt.") + exists(w, "logs/app1.log", "logs/app2.log", "logs/app3.log")
        + ([] if a[-1] == "red" else [f"arrow should stay red after false + cancel, got {a[-1]}"])
        + db_is(w, "select outcome, risk from turns", [("cancelled", "delete")])),
    "edit-does-not-run": (
        [("zshai: flytt sales.csv til Pictures\r", 2), ("e", 1)], None,
        lambda w, s, a: has(s, "mv sales.csv Pictures/") + exists(w, "sales.csv") + gone(w, "Pictures/sales.csv")
        + db_is(w, "select outcome from turns", [("edited",)])),
    "internal-error-runs-nothing": (
        [("zshai: slett alle loggfilene\r", 2), ("j", 1), ("\r", 1)], {"ZSHAI_TEST_FAULT": "1"},
        lambda w, s, a: has(s, "nothing was run") + exists(w, "logs/app1.log", "logs/app2.log", "logs/app3.log")),
    "danger-needs-typed-yes": (
        [("zshai: slett hjemmemappa\r", 2), ("j\r", 1)], None,
        lambda w, s, a: has(s, "farlig", "Skriv JA", "Avbrutt.") + exists(w, "sales.csv")),
    "install-suggestion": (
        [("zshai: gjør om ferie.mov til mp4\r", 2), ("n", 1)], None,
        lambda w, s, a: has(s, "Dette trenger ffmpeg", "install", "Avbrutt.")),
    "clarifying-question": (
        [("zshai: rydd opp\r", 2), ("loggfilene\r", 2)], None,
        lambda w, s, a: has(s, "Hva vil du rydde opp i", "? loggfilene", "ls -la logs", "app3.log")),
    "git-discard-backup-undo": (
        [("cd repo\r", 0.8), ("zshai: forkast alle endringene mine\r", 2), ("JA\r", 1.5), ("zshai: angre\r", 1.5),
         ("git status --short\r", 1)], None,
        lambda w, s, a: has(s, "forkaster endringene i 1 fil", "Ulagrede endringer er lagt tilbake", " M a.txt")
        + db_is(w, "select action, via from effects order by id", [("backup", "git"), ("restored", "undo")])),
    "cwd-replaced-follows-new-folder": (
        [("mkdir d && cd d\r", 0.6), ("rmdir ../d && mkdir ../d\r", 0.6), ("zshai: rydd opp\r", 2)], None,
        lambda w, s, a: has(s, "Hva vil du rydde opp i") + (["getcwd error shown"] if "getcwd" in s else [])),
    "cwd-gone-says-so": (
        [("mkdir d && cd d\r", 0.6), ("rmdir ../d\r", 0.6), ("zshai: rydd opp\r", 2)], None,
        lambda w, s, a: has(s, "finnes ikke lenger")),
    "plain-text-goes-to-shell-by-default": (
        [("hvor mange rader er det i sales.csv?\r", 1)], None,
        lambda w, s, a: ([] if "no matches found: sales.csv?" in s or "command not found: hvor" in s
                         else ["zsh did not get the line"]) + (["went to zshai"] if "tail -n" in s else [])),
    "auto-on-sends-plain-text": (
        [("hvor mange rader er det i sales.csv?\r", 2)], {"_cfg": {"autoDetect": True}},
        lambda w, s, a: has(s, "tail -n +2 sales.csv | wc -l", "\n3")),
    "auto-toggle-meta": (
        [("zshai: auto på\r", 0.8), ("hvor mange rader er det i sales.csv?\r", 2)], None,
        lambda w, s, a: has(s, "Automatisk gjenkjenning: på", "tail -n +2 sales.csv | wc -l")),
    "how-do-i-shows-not-runs": (
        [("zshai: hvordan sletter jeg loggfilene?\r", 2), ("\x1bOA", 0.6)], None,
        lambda w, s, a: has(s, "▸ rm logs/*.log", "ikke kjørt")
        + exists(w, "logs/app1.log", "logs/app2.log", "logs/app3.log")
        + ([] if ROWS_SEEN[-1].endswith("rm logs/*.log") else [f"↑ did not bring back the command: {ROWS_SEEN[-1]!r}"])),
    "explain-command": (
        [("zshai: forklar tar -xzvf a.tgz\r", 2)], None,
        lambda w, s, a: has(s, "▸ tar -xzvf a.tgz", "tar pakker ut (-x)", "viser hvert filnavn")
        + db_is(w, "select mode, outcome, exit_code from turns", [("show", "shown", None)])
        + colour("tar pakker ut", "ffd700")),  # gold on a dark background
    "light-terminal-detected": (
        [("zshai: forklar tar -xzvf a.tgz\r", 2.5)], {"ZSHAI_BG": "", "_bg_reply": b"\x1b]11;rgb:ffff/ffff/f0f0\x1b\\"},
        lambda w, s, a: colour("tar pakker ut", "005faf") + (["OSC reply leaked"] if "rgb:" in s else [])),
    "dark-terminal-detected": (
        [("zshai: forklar tar -xzvf a.tgz\r", 2.5)], {"ZSHAI_BG": "", "_bg_reply": b"\x1b]11;rgb:1e1e/1e1e/2e2e\x07"},
        lambda w, s, a: colour("tar pakker ut", "ffd700")),
    "silent-terminal-falls-back-to-gold": (
        [("zshai: forklar tar -xzvf a.tgz\r", 2.5)], {"ZSHAI_BG": ""},
        lambda w, s, a: colour("tar pakker ut", "ffd700")),
    "theme-picker": (
        [("zshai: tema\r", 1), ("4", 1), ("zshai: forklar tar -xzvf a.tgz\r", 2)], None,
        lambda w, s, a: has(s, "dracula  Dracula-farger", "Velg tema (1–8", "Tema: classic")
        + ([] if json.load(open(f"{w}/.cfg/config.json")).get("theme") == "classic" else ["theme not saved"])
        + colour("tar pakker ut", "brightwhite")),
    "language-by-name": (
        [("zshai: språk Engelsk\r", 0.8), ("zshai: help\r", 0.8), ("zshai: language norsk\r", 0.8)], None,
        lambda w, s, a: has(s, "Language: English\n", "type \"zshai:\" and what you want", "Språk: norsk\n")
        + ([] if json.load(open(f"{w}/.cfg/config.json")).get("language") == "no" else ["language not saved"])),
    "zai-shortcut": (
        [("zai: tema\r", 1), ("x", 0.5), ("zai: språk engelsk\r", 0.8), ("zai: hvor mange rader er det i sales.csv?\r", 2)], None,
        lambda w, s, a: has(s, "tail -n +2 sales.csv | wc -l", "\n3", "Velg tema", "Language: English\n")),
    "fix-after-failure": (
        [("zshai: tell linjene i salg.csv\r", 3), ("j", 1.5)], None,
        lambda w, s, a: has(s, "wc -l salg.csv", "No such file", "Kommandoen feilet (kode 1)", "▸ wc -l sales.csv",
                            "Kjøre den rettede kommandoen?", "4 sales.csv")
        + db_is(w, "select mode, exit_code, fixes, fix_of is null from turns order by id",
                [("run", 1, 1, 1), ("fix", 0, 0, 0)])),
    "fix-edit-does-not-run": (
        [("zshai: tell linjene i salg.csv\r", 3), ("e", 1)], None,
        lambda w, s, a: (["edited fix ran"] if "4 sales.csv" in s else [])
        + ([] if ROWS_SEEN[-1].endswith("wc -l sales.csv") else [f"fix not in the line: {ROWS_SEEN[-1]!r}"])),
    "no-fix-for-silent-failure": (
        [("zshai: finn ordet zebra i sales.csv\r", 3)], None,
        lambda w, s, a: has(s, "grep zebra sales.csv") + (["offered a fix for grep without match"] if "feilet" in s else [])
        + ([] if a[-1] == "red" else [f"arrow should be red after grep found nothing, got {a[-1]}"])),
    "fix-only-once": (
        [("zshai: vis fila notat.txt\r", 3), ("j", 3)], None,
        lambda w, s, a: has(s, "cat notat.txt", "▸ cat notat.md") + ([] if s.count("feilet") == 1 else [f"{s.count('feilet')} fix offers, want 1"])),
    "fix-when-exit-0-but-errors": (
        [("zshai: regn ut oppetiden i timer\r", 3), ("j", 1.5)], None,
        lambda w, s, a: has(s, "illegal time format", "Kommandoen skrev feilmeldinger", "▸ print 52", "\n52")
        + db_is(w, "select exit_code, fixes from turns order by id", [(0, 1), (0, 0)])),
    "no-fix-for-progress-on-stderr": (
        [("zshai: hent nettsiden\r", 3)], None,
        lambda w, s, a: has(s, "Receiving objects", "ferdig") + (["offered a fix for progress output"] if "Forslag" in s else [])),
    "stats-show-version-arch-and-numbers": (
        [("zshai: hvor mange rader er det i sales.csv?\r", 2), ("zshai: slett alle loggfilene\r", 2), ("n", 1),
         ("zai: stats\r", 1.5)], None,
        lambda w, s, a: has(s, "zshai 0.2.0 (", "linux/" if sys.platform.startswith("linux") else "darwin/",
                            "Statistikk", "forespørsler: 2", "kjørt: 1", "avbrutt: 1", "mest brukt:")),
    "meta-help-and-request-fallthrough": (
        [("zshai: hjelp\r", 0.8), ("zshai: status på repoet\r", 2)], None,
        lambda w, s, a: has(s, "zshai: hvordan lister jeg bare mapper?") + (["status meta swallowed a request"] if "model:" in s else [])),
    # zshai: prompt opens prompt.txt in $EDITOR; the edited prompt goes to the model
    "prompt-edit-reaches-model": (
        [("zshai: prompt\r", 1.5), ("zshai: hvor mange rader er det i sales.csv?\r", 2)],
        {"EDITOR": "sh -c 'echo \"- Liker korte svar (TEST-PREF).\" >> \"$1\"' ed"},
        lambda w, s, a: (["prompt.txt not edited"] if "TEST-PREF" not in open(f"{w}/.cfg/prompt.txt").read() else [])
        + ([] if SYSTEMS and "TEST-PREF" in SYSTEMS[-1] and "About this machine" in SYSTEMS[-1] and "{os}" not in SYSTEMS[-1]
           else ["edited prompt not sent to the model"]) + has(s, "\n3")),
    # a spinner with a waving label while the model works; gone when the answer comes
    "spinner-while-thinking": (
        [("zshai: tenk lenge\r", 0.7), ("", 1.5)], None,
        lambda w, s, a: ([] if "tenker…" in ROWS_SEEN[0].lower()  # the wave itself: spinner_test.go
                         else [f"no waving spinner while waiting: {ROWS_SEEN[0]!r}"])
        + has(s, "ferdig") + (["spinner left on screen"] if "tenker…" in s.lower() else [])),
    "spinner-says-retrying": (
        [("zshai: prøv igjen\r", 1.6), ("", 1.5)], None,
        lambda w, s, a: ([] if "prøver igjen…" in ROWS_SEEN[0].lower() else [f"retry not shown: {ROWS_SEEN[0]!r}"])
        + has(s, "ferdig")),
    # the model's jq | column table recipe comes out as a PowerShell-style table
    "table-powershell-style": (
        [("zshai: vis ordrene som tabell\r", 2)], None,
        lambda w, s, a: has(s, "zshai fmt -t orders.json", "id customer     total", "-- --------     -----",
                            " 1 Kari         250.5", "12 Ola Nordmann     9")),
    "shell-lines-stay-shell": (
        [("ls\r", 0.6), ("69\r", 0.6), ("gst\r", 0.8), (",echo komma\r", 0.6)], {"_cfg": {"autoDetect": True}},
        lambda w, s, a: has(s, "sales.csv", "command not found: 69", "komma")
        + ([] if "…" not in s else ["a shell line went to zshai"])),
}


def main():
    want = sys.argv[1:] or list(SCENARIOS)
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FakeModel)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    port = srv.server_address[1]
    failed = 0
    for name in want:
        steps, env, check = SCENARIOS[name]
        work = tempfile.mkdtemp(prefix=f"zshai-{name}-")
        try:
            fixtures(work)
            screen, arrows = drive(work, steps, port, env)
            problems = check(work, screen, arrows)
        except Exception as e:  # pragma: no cover
            problems, screen = [f"crashed: {e}"], ""
        status = "ok  " if not problems else "FAIL"
        print(f"{status} {name}")
        if problems:
            failed += 1
            for p in problems:
                print(f"     - {p}")
            print("     screen:\n" + "\n".join("       | " + l for l in screen.splitlines()[-25:]))
        shutil.rmtree(work, ignore_errors=True)
    srv.shutdown()
    print(f"\n{len(want) - failed}/{len(want)} scenarios passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
