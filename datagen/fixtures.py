#!/usr/bin/env python3
"""Fixture homes for datagen: a fake $HOME with documents, pictures, downloads, git
projects, data files and logs. Names, counts and dates vary with the seed, so the model
learns to read "ls:"/"named:" instead of memorising one folder.

    python3 datagen/fixtures.py DIR SEED      # builds DIR (must not exist)

stdlib only (runs on macOS' /usr/bin/python3 and on Ubuntu).
"""
import json, os, random, subprocess, sys, time

def main(root, seed):
    R = random.Random(seed)
    now = time.time()
    os.makedirs(root)

    def put(rel, data=b"", days=None, size=None):
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "wb") as f:
            if size is not None:  # real bytes at the start, the rest sparse (ls shows the size, copies stay cheap)
                f.write(bytes(R.getrandbits(8) for _ in range(min(size, 4096))))
                f.truncate(size)
            else:
                f.write(data if isinstance(data, bytes) else data.encode())
        t = now - (days if days is not None else R.uniform(0, 90)) * 86400
        os.utime(p, (t, t))

    pick = lambda xs, k: R.sample(xs, min(k, len(xs)))
    KB = 1024; MB = 1024 * KB

    # --- ssh hosts (names only, like a real ~/.ssh/config) ---
    hosts = pick(["nas", "web1", "pi", "devbox", "backup", "build", "media"], R.randint(1, 3))
    put(".ssh/config", "".join(f"Host {h}\n  HostName {h}.lan\n  User me\n\n" for h in hosts), days=30)

    # --- Documents ---
    docs = ["rapport-2026.pdf", "CV.docx", "budsjett.xlsx", "notes.txt", "Møtereferat 3. nov.md", "kontrakt signert.pdf",
            "presentation.pptx", "todo.md", "letter.odt", "invoice-1042.pdf", "tax return 2025.pdf", "manual.pdf"]
    for d in pick(docs, R.randint(4, 9)):
        put("Documents/" + d, size=R.randint(8 * KB, 3 * MB))
    for i in range(R.randint(2, 6)):
        put(f"Documents/invoices/faktura-{2026}{R.randint(1,11):02d}-{i+1:02d}.pdf", size=R.randint(20 * KB, 400 * KB))
    put("Documents/notes.txt", "kjøp melk\nring tannlegen\nTODO: send rapport til Ola\nbackup av nas\n", days=2)

    # --- Pictures ---
    pext = R.choice([".jpg", ".JPG", ".heic"])
    for i in range(R.randint(6, 18)):
        put(f"Pictures/IMG_{R.randint(1000, 9999)}{pext}", size=R.randint(200 * KB, 4 * MB), days=R.uniform(0, 60))
    for i in range(R.randint(1, 5)):
        put(f"Pictures/Screenshot 2026-11-{R.randint(1,14):02d} at {R.randint(8,22)}.{R.randint(10,59)}.png", size=R.randint(100 * KB, 900 * KB))
    trip = R.choice(["ferie", "holiday 2025", "Lofoten", "wedding"])
    for i in range(R.randint(2, 6)):
        put(f"Pictures/{trip}/DSC_{R.randint(100, 999)}.jpg", size=R.randint(1 * MB, 5 * MB), days=R.uniform(100, 400))

    # --- Downloads ---
    dl = ["installer.dmg", "setup.deb", "archive.zip", "copy of report.pdf", "a.tmp", "b.tmp", "data-export.csv",
          "song.mp3", "movie.mp4", "photo (1).jpg", "photo (2).jpg", "font.ttf", "ubuntu-26.04-desktop-amd64.iso", "invoice.pdf"]
    for d in pick(dl, R.randint(5, 11)):
        big = d.endswith((".iso", ".dmg", ".mp4"))
        put("Downloads/" + d, size=R.randint(5 * MB, 30 * MB) if big else R.randint(1 * KB, 2 * MB), days=R.uniform(0, 120))

    # --- media ---
    if R.random() < 0.7:
        for m in pick(["holiday.mov", "clip.mp4", "screen recording.mov", "talk.mkv"], R.randint(1, 3)):
            put("Movies/" + m, size=R.randint(3 * MB, 12 * MB))
    if R.random() < 0.6:
        for m in pick(["01 Intro.mp3", "02 Song.mp3", "voice memo.m4a", "track.flac", "podcast-ep12.mp3"], R.randint(2, 4)):
            put("Music/" + m, size=R.randint(1 * MB, 6 * MB))

    # --- data files ---
    rng = R.randint(4, 9)
    users = [{"name": n, "email": n.split()[0].lower() + "@example.no", "age": R.randint(20, 70), "city": R.choice(["Oslo", "Bergen", "Tromsø", "Trondheim"]),
              "active": R.random() < 0.7} for n in pick(["Kari Nordmann", "Ola Hansen", "Per Berg", "Anne Lie", "Jonas Dahl", "Ingrid Moe", "Lars Bakke", "Nina Holm"], rng)]
    put("data/users.json", json.dumps(users, indent=2, ensure_ascii=False))
    with open(os.path.join(root, "data/events.jsonl"), "w") as f:
        for i in range(R.randint(8, 25)):
            f.write(json.dumps({"ts": f"2026-11-{R.randint(1,14):02d}T{R.randint(0,23):02d}:{R.randint(0,59):02d}:00", "level": R.choice(["info", "warn", "error"]),
                                "service": R.choice(["api", "web", "worker"]), "ms": R.randint(1, 900)}) + "\n")
    rows = ["dato,produkt,antall,beløp"] + [f"2026-{R.randint(1,11):02d}-{R.randint(1,28):02d},{R.choice(['kaffe','te','kake','bolle'])},{R.randint(1,9)},{R.randint(20,500)}.{R.choice(['00','50'])}" for _ in range(R.randint(8, 30))]
    put("data/" + R.choice(["sales.csv", "salg.csv", "orders.csv"]), "\n".join(rows) + "\n")
    put("data/contacts.csv", "navn;by;telefon\n" + "".join(f"{u['name']};{u['city']};9{R.randint(1000000,9999999)}\n" for u in users))
    put("data/report.tsv", "month\trevenue\tcost\n" + "".join(f"{m}\t{R.randint(50,200)*1000}\t{R.randint(30,120)*1000}\n" for m in ["jan", "feb", "mar", "apr"]))
    put("data/config.json", json.dumps({"server": {"host": "192.0.2.10", "port": R.choice([80, 8080, 1234])}, "debug": R.random() < 0.5, "features": ["a", "b"]}))
    put("data/settings.yaml", "server:\n  host: 192.0.2.10\n  port: 8080\nretries: 3\nthemes:\n  - gold\n  - nord\n")

    # --- logs ---
    lv = ["INFO", "INFO", "WARN", "ERROR", "DEBUG", "INFO"]
    lines = [f"2026-11-{R.randint(10,14):02d} {R.randint(0,23):02d}:{R.randint(0,59):02d}:{R.randint(0,59):02d} {R.choice(lv)} {R.choice(['worker-1','worker-2','api','db'])}: "
             f"{R.choice(['request done','timeout talking to db','user login','cache miss','disk almost full','retrying job'])} id={R.randint(1,999)}" for _ in range(R.randint(40, 200))]
    lines.sort()
    put("logs/app.log", "\n".join(lines) + "\n", days=0.1)
    put("logs/app.log.1", "\n".join(lines[: len(lines) // 2]) + "\n", days=8)
    put("logs/error.log", "\n".join(l for l in lines if " ERROR " in l) + "\n", days=0.2)
    for i in range(R.randint(1, 4)):
        put(f"logs/app.log.{i+2}.gz", size=R.randint(5 * KB, 80 * KB), days=10 + 7 * i)

    # --- projects (git) ---
    def git(d, *args):
        subprocess.run(["git", "-C", d, *args], check=True, capture_output=True,
                       env={**os.environ, "GIT_AUTHOR_NAME": "Dev", "GIT_AUTHOR_EMAIL": "dev@example.com",
                            "GIT_COMMITTER_NAME": "Dev", "GIT_COMMITTER_EMAIL": "dev@example.com", "HOME": root})
    web = R.choice(["webapp", "site", "shop-frontend"])
    put(f"projects/{web}/package.json", json.dumps({"name": web, "version": "1.2.0", "scripts": {"start": "node src/index.js", "test": "jest"},
                                                     "dependencies": {"express": "^4.19.0"}}, indent=2))
    put(f"projects/{web}/src/index.js", "const express = require('express');\n// TODO: add auth\nconst app = express();\napp.listen(3000);\n")
    put(f"projects/{web}/src/util.js", "export function sum(a, b) { return a + b; }\n// FIXME: handle NaN\n")
    put(f"projects/{web}/README.md", f"# {web}\n\nRun with npm start.\n")
    put(f"projects/{web}/.gitignore", "node_modules/\n.env\n")
    d = os.path.join(root, "projects", web)
    git(d, "init", "-q", "-b", "main"); git(d, "add", "-A"); git(d, "commit", "-qm", "first version")
    put(f"projects/{web}/src/index.js", "const express = require('express');\n// TODO: add auth\nconst app = express();\napp.get('/', (q, r) => r.send('hi'));\napp.listen(3000);\n")
    git(d, "commit", "-qam", "add root route")
    if R.random() < 0.6:
        git(d, "checkout", "-qb", R.choice(["feature/login", "fix-typo", "dev"]))
        put(f"projects/{web}/src/login.js", "// login\n")
        git(d, "add", "-A"); git(d, "commit", "-qm", "login page")
        git(d, "checkout", "-q", "main")
    if R.random() < 0.6:
        put(f"projects/{web}/src/util.js", "export function sum(a, b) { return Number(a) + Number(b); }\n")  # modified
    if R.random() < 0.4:
        put(f"projects/{web}/notes.tmp", "scratch\n")  # untracked
    tool = R.choice(["tool", "scraper", "backup-script"])
    put(f"projects/{tool}/main.py", "import sys\n\ndef main():\n    print('hello', sys.argv[1:])\n\nif __name__ == '__main__':\n    main()\n")
    put(f"projects/{tool}/requirements.txt", "requests==2.32.0\nrich\n")
    put(f"projects/{tool}/tests/test_main.py", "def test_ok():\n    assert True\n")
    d = os.path.join(root, "projects", tool)
    git(d, "init", "-q", "-b", R.choice(["main", "master"])); git(d, "add", "-A"); git(d, "commit", "-qm", "initial")
    put(f"projects/{tool}/main.py", "import sys\n\ndef main():\n    print('hello', sys.argv[1:])  # TODO args\n\nif __name__ == '__main__':\n    main()\n")
    put("projects/scripts/backup.sh", "#!/bin/sh\nrsync -a ~/Documents nas:backup/\n")
    put("projects/scripts/cleanup.sh", "#!/bin/sh\nfind ~/Downloads -name '*.tmp' -delete\n")

    # --- archives / misc ---
    put(f"backup/documents-2026-{R.randint(1,10):02d}.tar.gz", size=R.randint(1 * MB, 8 * MB), days=R.uniform(20, 80))
    if R.random() < 0.5:
        put("backup/old photos.zip", size=R.randint(2 * MB, 10 * MB), days=200)
    put(".zshrc", "export ZSH=$HOME/.oh-my-zsh\nZSH_THEME=robbyrussell\nplugins=(git)\nsource $ZSH/oh-my-zsh.sh\n", days=40)
    put(".gitconfig", "[user]\n\tname = Dev\n\temail = dev@example.com\n", days=40)
    return hosts

if __name__ == "__main__":
    print(" ".join(main(sys.argv[1], int(sys.argv[2]))))
