#!/usr/bin/env python3
"""Table and list examples with known answers (zshai fmt), no teacher needed.

The 3B v3 model learned "show X as a table" but not the rest of zshai fmt: picking columns
(-p), sorting by the file's real column (--sort -col), top N (-n), filtering first (jq / awk)
and then formatting. The right answer to these is mechanical, so it is written from
templates here, for the files in the training worlds (seeds 1-5; evaluation uses 9-10),
plus a "tables" folder per world with more varied files (English and Norwegian headers,
, ; and tab delimiters, JSON arrays and JSON Lines).

    python3 datagen/gen_fmt.py WORLDS OUT.jsonl ORACLE.json      # requests + answers
    python3 datagen/gen_fmt.py --serve ORACLE.json PORT          # a stand-in "model" that answers them

Then run_teacher.py --server http://127.0.0.1:PORT/v1 runs every request through the real
zshai (exact prompt, all checks, the command run on the real files); what passes is kept.
"""
import csv, http.server, io, json, os, random, re, sys

SEEDS = [1, 2, 3, 4, 5]

# extra files per world: name -> (columns, row maker)
def extra_files(R):
    names = ["Ingrid Moe", "Ola Hansen", "Per Berg", "Nina Holm", "Lars Bakke", "Kari Nordmann", "Anne Lie", "Jonas Dahl",
             "Alice Smith", "Bob Jones", "Carol White", "Dan Brown"]
    cities = ["Oslo", "Bergen", "Tromsø", "Trondheim", "Stavanger"]
    n = R.randint(6, 12)
    return {
        "orders.csv": (",", ["order_id", "customer", "product", "amount", "status"],
                       [[1000 + i, R.choice(names), R.choice(["laptop", "mouse", "screen", "cable", "dock"]),
                         f"{R.randint(50, 9000)}.00", R.choice(["paid", "pending", "shipped"])] for i in range(n)]),
        "produkter.csv": (";", ["navn", "kategori", "pris", "lager"],
                          [[p, R.choice(["kontor", "data", "kjøkken"]), R.randint(20, 3000), R.randint(0, 80)]
                           for p in R.sample(["stol", "pult", "lampe", "kopp", "mus", "skjerm", "kabel", "tavle", "hylle", "vifte"], n - 2)]),
        "inventory.tsv": ("\t", ["item", "qty", "location"],
                          [[i, R.randint(0, 200), R.choice(["A1", "A2", "B1", "C3"])]
                           for i in R.sample(["bolts", "nuts", "screws", "washers", "hinges", "nails", "brackets", "rivets"], 6)]),
        "servers.json": ("json", ["name", "ip", "os", "cpu", "memory_gb", "status"],
                         [[f"srv-{i:02d}", f"192.0.2.{10 + i}", R.choice(["ubuntu", "debian", "rhel"]), R.choice([2, 4, 8, 16]),
                           R.choice([4, 8, 16, 32, 64]), R.choice(["up", "up", "down"])] for i in range(n)]),
        "ansatte.json": ("json", ["navn", "avdeling", "alder", "by", "aktiv"],
                         [[nm, R.choice(["salg", "it", "lager", "økonomi"]), R.randint(22, 64), R.choice(cities), R.choice([True, True, False])]
                          for nm in R.sample(names, n - 3)]),
        "logins.jsonl": ("jsonl", ["user", "time", "ip", "success"],
                         [[R.choice(["anna", "ola", "kari", "dev", "admin"]), f"2026-11-{R.randint(1, 16):02d}T{R.randint(0, 23):02d}:{R.randint(0, 59):02d}:00",
                           f"198.51.100.{R.randint(2, 250)}", R.choice([True, True, False])] for _ in range(n)]),
    }

def write_file(path, spec):
    kind, cols, rows = spec
    if kind in (",", ";", "\t"):
        with open(path, "w", newline="") as f:
            w = csv.writer(f, delimiter=kind, lineterminator="\n")
            w.writerow(cols)
            w.writerows(rows)
    elif kind == "json":
        json.dump([dict(zip(cols, r)) for r in rows], open(path, "w"), indent=2, ensure_ascii=False)
    else:
        with open(path, "w") as f:
            for r in rows:
                f.write(json.dumps(dict(zip(cols, r)), ensure_ascii=False) + "\n")

def read_table(path):
    """(delimiter|json|jsonl, columns, rows as dicts) for a data file."""
    ext = os.path.splitext(path)[1]
    text = open(path, encoding="utf-8").read()
    if ext == ".json":
        v = json.loads(text)
        if not isinstance(v, list) or not v or not isinstance(v[0], dict):
            return None
        return "json", list(v[0].keys()), v
    if ext == ".jsonl":
        rows = [json.loads(l) for l in text.splitlines() if l.strip()]
        return "jsonl", list(rows[0].keys()), rows
    first = text.split("\n", 1)[0]
    d = "\t" if ext == ".tsv" else (";" if first.count(";") > first.count(",") else ",")
    rows = list(csv.DictReader(io.StringIO(text), delimiter=d))
    return d, list(rows[0].keys()), rows

def numeric(rows, c):
    try:
        return all(isinstance(r[c], (int, float)) and not isinstance(r[c], bool) or (isinstance(r[c], str) and re.fullmatch(r"-?\d+(\.\d+)?", r[c])) for r in rows)
    except KeyError:
        return False

# how people name a column that is called this in the file
SYN = {"amount": ["amount", "beløp", "sum"], "beløp": ["beløp", "amount", "sum"], "pris": ["pris", "price"],
       "navn": ["navn", "name"], "name": ["name", "navn"], "email": ["email", "e-post"], "city": ["city", "by"],
       "by": ["by", "city"], "age": ["age", "alder"], "alder": ["alder", "age"], "qty": ["qty", "quantity", "antall"],
       "antall": ["antall", "quantity"], "memory_gb": ["memory", "minne", "memory_gb"], "ts": ["ts", "tid", "time"],
       "level": ["level", "nivå"], "month": ["month", "måned"], "revenue": ["revenue", "inntekt"], "cost": ["cost", "kostnad"],
       "telefon": ["telefon", "phone"], "customer": ["customer", "kunde"], "status": ["status"], "lager": ["lager", "stock"]}

def say(c, R):
    return R.choice(SYN.get(c, [c]))

def lit(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    return json.dumps(v, ensure_ascii=False)

def examples(f, kind, cols, rows, R):
    """(lang, request, command) for one file."""
    out = []
    nums = [c for c in cols if numeric(rows, c)]
    texts = [c for c in cols if c not in nums]
    add = lambda no, en, cmd: out.append(("no", no, cmd) if R.random() < 0.5 else ("en", en, cmd))
    add(R.choice([f"vis {f} som en tabell", f"vis {f} i tabellform", f"lag en tabell av {f}", f"kan du vise {f} som tabell"]),
        R.choice([f"show {f} as a table", f"display {f} in a table", f"format {f} as a table", f"can you show {f} as a table"]),
        f"zshai fmt -t {f}")
    add(R.choice([f"vis {f} som en liste", f"vis hver rad i {f} for seg", f"list opp {f} som liste"]),
        R.choice([f"show {f} as a list", f"list {f} one record at a time", f"show each entry in {f} as a list"]),
        f"zshai fmt -l {f}")
    if len(cols) >= 3:
        a, b = R.sample(cols, 2)
        a, b = sorted((a, b), key=cols.index)
        add(R.choice([f"vis bare {say(a, R)} og {say(b, R)} fra {f}", f"vis kolonnene {say(a, R)} og {say(b, R)} i {f}"]),
            R.choice([f"show only {say(a, R)} and {say(b, R)} from {f}", f"show the {say(a, R)} and {say(b, R)} columns of {f}"]),
            f"zshai fmt -t -p {a},{b} {f}")
    if nums:
        n = R.choice(nums)
        add(R.choice([f"sorter {f} etter {say(n, R)}, størst først", f"vis {f} sortert etter {say(n, R)} synkende"]),
            R.choice([f"show {f} sorted by {say(n, R)}, highest first", f"sort {f} by {say(n, R)} descending"]),
            f"zshai fmt -t --sort -{n} {f}")
        add(R.choice([f"sorter {f} etter {say(n, R)}", f"vis {f} sortert på {say(n, R)}, minst først"]),
            R.choice([f"sort {f} by {say(n, R)}", f"show {f} sorted by {say(n, R)}, lowest first"]),
            f"zshai fmt -t --sort {n} {f}")
        k = R.choice([3, 5])
        add(f"vis de {k} radene med høyest {say(n, R)} i {f}", f"show the top {k} rows of {f} by {say(n, R)}",
            f"zshai fmt -t --sort -{n} -n {k} {f}")
        if texts:
            a = R.choice(texts)
            add(f"vis {say(a, R)} og {say(n, R)} fra {f}, sortert etter {say(n, R)} størst først",
                f"show {say(a, R)} and {say(n, R)} from {f} sorted by {say(n, R)}, biggest first",
                f"zshai fmt -t -p {a},{n} --sort -{n} {f}")
    k = R.choice([3, 5])
    add(f"vis de første {k} radene i {f} som tabell", f"show the first {k} rows of {f} as a table", f"zshai fmt -t -n {k} {f}")
    # filter, then format
    cands = [c for c in cols if len({str(r[c]) for r in rows}) in range(2, 5)]
    if cands:
        c = R.choice(cands)
        v = R.choice(sorted({r[c] for r in rows}, key=str))
        vs = lit(v) if not isinstance(v, str) else v
        if kind == "json":
            cmd = f"jq '[.[] | select(.{c} == {lit(v)})]' {f} | zshai fmt -t"
        elif kind == "jsonl":
            cmd = f"jq -c 'select(.{c} == {lit(v)})' {f} | zshai fmt -t"
        else:
            i = cols.index(c) + 1
            fs = {",": "-F,", ";": "-F';'", "\t": "-F'\\t'"}[kind]
            cmd = f"awk {fs} 'NR==1 || ${i}==\"{v}\"' {f} | zshai fmt -t --csv"
        add(R.choice([f"vis radene i {f} der {say(c, R)} er {vs}, som tabell", f"vis bare de med {say(c, R)} {vs} fra {f} i en tabell"]),
            R.choice([f"show the rows in {f} where {say(c, R)} is {vs} as a table", f"table of {f} with only {say(c, R)} = {vs}"]),
            cmd)
    return out

def generate(worlds, out, oracle):
    reqs, answers = [], {}
    for s in SEEDS:
        R = random.Random(s * 101)
        home = os.path.join(worlds, f"w{s}")
        tdir = os.path.join(home, "tables")
        os.makedirs(tdir, exist_ok=True)
        for name, spec in extra_files(R).items():
            write_file(os.path.join(tdir, name), spec)
        for cwd in ("data", "tables"):
            for f in sorted(os.listdir(os.path.join(home, cwd))):
                if not re.search(r"\.(csv|tsv|json|jsonl)$", f):
                    continue
                t = read_table(os.path.join(home, cwd, f))
                if not t:
                    continue
                for lang, req, cmd in examples(f, *t, R):
                    key = f"{s}|{cwd}|{req}"
                    if key in answers:
                        continue
                    answers[key] = cmd
                    reqs.append({"seed": s, "cwd": cwd, "lang": lang, "cat": "fmt", "request": req})
    with open(out, "w") as o:
        for r in reqs:
            o.write(json.dumps(r, ensure_ascii=False) + "\n")
    json.dump(answers, open(oracle, "w"), ensure_ascii=False, indent=0)
    print(f"{len(reqs)} requests")

def serve(oracle, port):
    answers = json.load(open(oracle))
    by_req = {}
    for k, v in answers.items():
        s, cwd, req = k.split("|", 2)
        by_req.setdefault(req, []).append((cwd, v))

    class H(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            first = next(m["content"] for m in body["messages"] if m["role"] == "user")
            req = (re.search(r"^request: (.*)$", first, re.M) or [None, ""])[1]
            cwd = (re.search(r"^cwd: ~/?(.*)$", first, re.M) or [None, ""])[1]
            cands = by_req.get(req, [])
            cmd = next((v for c, v in cands if c == cwd), cands[0][1] if cands else "")
            text = f"r: read\nm: run\na: \nn: \nx: 0\n---\n{cmd}" if cmd else "r: read\nm: run\na: \nn: \nx: 0\n---\n"
            out = json.dumps({"choices": [{"message": {"role": "assistant", "content": text}, "finish_reason": "stop"}],
                              "usage": {"prompt_tokens": 1, "completion_tokens": 1}}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(out)))
            self.end_headers()
            self.wfile.write(out)

        def log_message(self, *a):
            pass

    http.server.ThreadingHTTPServer(("127.0.0.1", int(port)), H).serve_forever()

if __name__ == "__main__":
    if sys.argv[1] == "--serve":
        serve(sys.argv[2], sys.argv[3])
    else:
        generate(*sys.argv[1:4])
