import json, os, random
import sys
d = sys.argv[1] if len(sys.argv) > 1 else "practice-files"; os.makedirs(d, exist_ok=True); os.chdir(d)
random.seed(1)
cmds = ["/sbin/launchd", "/usr/libexec/logd", "/Applications/Ghostty.app/Contents/MacOS/ghostty", "zsh -l", "/usr/sbin/sshd -D", "LM Studio Helper --type=gpu", "python3 server.py --port 8080"]
with open("running_processes.json", "w") as f:
    for i, c in enumerate(cmds):
        f.write(json.dumps({"pid": 100 + i * 37, "ppid": 1, "user": "alice" if i > 2 else "root", "cpu": round(random.random() * 30, 1), "mem": round(random.random() * 8, 1), "command": c}) + "\n")
json.dump([{"name": "Kari Nordmann", "email": "kari@example.no", "age": 34, "roles": ["admin", "dev"]},
           {"name": "Ola Hansen", "email": "ola@example.no", "age": 41, "roles": ["dev"]},
           {"name": "Per Berg", "email": "per@example.no", "age": 29, "roles": []}], open("users.json", "w"), indent=2)
json.dump({"server": {"host": "192.0.2.10", "port": 1234}, "model": "qwen/qwen3-coder-30b", "features": {"fix": True, "auto": False}, "notes": ["a", "b"]}, open("config.json", "w"), separators=(",", ":"))
open("sales.csv", "w").write("dato,produkt,antall,beløp\n2026-11-01,kaffe,3,147.50\n2026-11-02,te,1,39.00\n2026-11-02,kaffe,2,98.00\n2026-11-03,kake,4,220.00\n2026-11-04,te,2,78.00\n")
open("data_semicolon.csv", "w").write("navn;by;postnr\nKari;Oslo;0150\nOla;Bergen;5003\nPer Berg;Trondheim;7010\n")
open("report.tsv", "w").write("måned\tinntekt\tkostnad\njanuar\t120000\t80000\nfebruar\t95000\t70000\nmars\t130000\t85000\n")
lv = ["INFO", "INFO", "WARN", "ERROR", "DEBUG", "INFO"]
with open("app.log", "w") as f:
    for i in range(60):
        f.write(f"2026-11-14 10:{i:02d}:00 {lv[i % 6]} worker-{i % 3}: message number {i}\n")
open("settings.yaml", "w").write("server:\n  host: 192.0.2.10\n  port: 1234\nmodel: qwen/qwen3-coder-30b\nthemes:\n  - gold\n  - nord\n")
open("notes.md", "w").write("# Notater\n\n- kjøp kaffe\n- **viktig**: ring Ola\n\n## Kode\n\n```sh\nls -la\n```\n")
print("ok", sorted(os.listdir(".")))
