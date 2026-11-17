#!/usr/bin/env python3
"""Kept examples (run_teacher.py output) -> MLX-LM chat data for zshai-mini.

    python3 datagen/build_dataset.py OUTDIR [--drop-cats csv,json:ex-old.jsonl …] ex-linux.jsonl ex-mac.jsonl repairs.jsonl ...

Writes OUTDIR/train.jsonl and OUTDIR/valid.jsonl: {"messages": [system, user, assistant]}, or for
repair examples (gen_repairs.py) [system, user, assistant (failed), user (zshai's feedback), assistant].
--drop-cats C1,C2:FILE leaves those categories out of FILE (round 1's csv/json examples taught
jq … | column tables; round 2 re-ran them with zshai fmt).
The system prompt must stay byte-identical to llm.SystemMini (checked against llm.go).
"""
import json, os, random, re, sys

HERE = os.path.dirname(os.path.abspath(__file__))

def system_mini():
    src = open(os.path.join(HERE, "..", "internal", "llm", "llm.go")).read()
    m = re.search(r'const SystemMini = ((?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+)', src)
    return "".join(json.loads(p) for p in re.findall(r'"(?:[^"\\]|\\.)*"', m.group(1)))

def default_template():
    src = open(os.path.join(HERE, "..", "internal", "prompt", "prompt.go")).read()
    return re.search(r"const Default = `(.*?)`", src, re.S).group(1)

# Round 2 filters: the teacher (qwen3-coder) doesn't always follow the newer rules; examples
# that would teach the old habit are left out rather than learned.
TABLE_ASK = re.compile(r"(?i)\b(?:som|as|i|in)\s+(?:en\s+|a\s+)?(?:tabell|table|liste|list)\b|\btabell\b|\btable\b")
CONTENT_ASK = re.compile(r"(?i)\b(med|with|about|om|som inneholder|containing)\b")

def unwanted(j):
    """Why an example teaches the wrong thing, or ""."""
    body = j["target"].split("\n---\n", 1)[-1].strip()
    req = j.get("request") or (re.search(r"^request: (.*)$", j["prompt"], re.M) or [None, ""])[1]
    if j.get("cat") in ("csv", "json", "fmt") and TABLE_ASK.search(req) and body and "zshai fmt" not in body:
        return "table/list without zshai fmt"
    if j.get("cat") == "create" and body and CONTENT_ASK.search(req) and \
            re.fullmatch(r"(?:mkdir -p \S+ && )?touch \S+", body):
        return "an empty file for a request with content"
    return ""

USERS = ["erik", "kari", "ola", "anna", "dev", "admin", "jonas", "user", "sam", "lee"]
HOSTS = ["MacBook-Pro", "Mac", "server", "nas", "laptop", "devbox", "pi", "ubuntu", "arch", "mini"]

def render_system(tmpl, core, prompt, osname, lang, R):
    """prompt.Render in Python: the default template with this example's machine."""
    g = lambda k: (re.search(rf"^{k}: (.*)$", prompt, re.M) or [None, ""])[1]
    os_line = g("os")
    osn, _, flavour = os_line.partition("; ")
    v = {"os": osn, "flavour": flavour, "packages": g("packages") or "none",
         "arch": "arm64" if osname == "darwin" else R.choice(["x86_64", "x86_64", "arm64"]),
         "shell": "zsh " + R.choice(["5.9", "5.9", "5.8.1"]), "language": "Norwegian Bokmål" if lang == "no" else "English",
         "user": R.choice(USERS), "host": R.choice(HOSTS), "home": "~"}
    lines = [l for l in tmpl.split("\n") if not l.strip().startswith("#")]
    if R.random() < 0.1:  # some users delete the machine section
        lines = ["{zshai}"]
    out = "\n".join(lines).strip()
    return re.sub(r"\{([a-z]+)\}", lambda m: core.strip() if m.group(1) == "zshai" else v.get(m.group(1), m.group(0)), out)

def main():
    out, args = sys.argv[1], sys.argv[2:]
    drop, files = {}, []
    i = 0
    while i < len(args):
        if args[i] == "--drop-cats":
            cats, _, f = args[i + 1].partition(":")
            drop[f] = set(cats.split(","))
            files.append(f)
            i += 2
        else:
            files.append(args[i])
            i += 1
    core, tmpl = system_mini(), default_template()
    RS = random.Random(11)
    seen, rows = set(), []
    stats = {}
    for f in files:
        for line in open(f):
            j = json.loads(line)
            if j.get("cat") in drop.get(f, ()):
                continue
            if "zshai-dg-" in j["prompt"] or "/tmp/" in j["target"] or not j.get("target"):
                continue
            if why := unwanted(j):
                stats["dropped: " + why] = stats.get("dropped: " + why, 0) + 1
                continue
            key = (j["os"], j["prompt"], j.get("bad", ""))
            if key in seen:
                continue
            seen.add(key)
            # clarifying questions are rare in the data but matter most for safety: weight them up
            reps = 4 if j["verified"] == "ask-judged" else 1
            for _ in range(reps):
              msgs = [{"role": "system", "content": render_system(tmpl, core, j["prompt"], j["os"], j["lang"], RS)},
                      {"role": "user", "content": j["prompt"]}]
              if j.get("bad"):
                  msgs += [{"role": "assistant", "content": j["bad"]}, {"role": "user", "content": j["feedback"]}]
              msgs.append({"role": "assistant", "content": j["target"]})
              rows.append({"seed": j["seed"], "messages": msgs})
            k = f'{j["os"]}/{j["verified"]}'
            stats[k] = stats.get(k, 0) + 1
    R = random.Random(7)
    R.shuffle(rows)
    nvalid = max(20, len(rows) // 20)
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "valid.jsonl"), "w") as v, open(os.path.join(out, "train.jsonl"), "w") as t:
        for i, r in enumerate(rows):
            r.pop("seed")
            (v if i < nvalid else t).write(json.dumps(r, ensure_ascii=False) + "\n")
    print(f"{len(rows) - nvalid} train, {nvalid} valid;", ", ".join(f"{k} {v}" for k, v in sorted(stats.items())))

if __name__ == "__main__":
    main()
