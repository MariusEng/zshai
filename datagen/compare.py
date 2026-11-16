#!/usr/bin/env python3
"""Score a model against the teacher on the same requests (both run with run_teacher.py).

    python3 datagen/compare.py teacher.jsonl teacher.err student.jsonl student.err

valid  = the student's answer passed zshai's checks and ran without errors (or was a
         question / show / needs-a-program answer)
match  = it did what the teacher did: same output for read commands, the same files
         afterwards for changes, the same kind of answer otherwise
first  = valid without a repair round (one model call)
"""
import collections, json, sys

def load(ok, err):
    recs = {}
    for f, kept in ((ok, True), (err, False)):
        for l in open(f):
            if not l.startswith("{"):
                continue
            j = json.loads(l)
            j["kept"] = kept
            recs[(j["seed"], j["cwd"], j["request"])] = j
    return recs

def kind(j):
    return {"ran": "run", "ran-copy": "run", "not-run": "run"}.get(j.get("verified", ""), j.get("verified", ""))

def main():
    t, s = load(sys.argv[1], sys.argv[2]), load(sys.argv[3], sys.argv[4])
    keys = [k for k in t if t[k]["kept"] and k in s]
    tot = collections.Counter(); per = collections.defaultdict(collections.Counter)
    for k in keys:
        a, b = t[k], s[k]
        valid = b["kept"]
        match = valid and kind(a) == kind(b) and (
            (a.get("out") is None or a.get("out") == b.get("out")) and (a.get("tree") is None or a.get("tree") == b.get("tree"))
            or a.get("target", "").split("---\n")[-1].strip() == b.get("target", "").split("---\n")[-1].strip())
        for c in (tot, per[a["cat"]], per["lang:" + a["lang"]]):
            c["n"] += 1; c["valid"] += valid; c["match"] += bool(match); c["first"] += valid and b.get("calls") == 1
    def line(name, c):
        n = c["n"] or 1
        return f"{name:12} n={c['n']:4}  valid {c['valid']/n:.2f}  match {c['match']/n:.2f}  first-try {c['first']/n:.2f}"
    print(line("ALL", tot))
    for k in sorted(per):
        print(line(k, per[k]))
    secs = [s[k]["seconds"] for k in keys if s[k].get("seconds")]
    if secs:
        secs.sort()
        print(f"student seconds per request: p50 {secs[len(secs)//2]:.2f}  p90 {secs[int(len(secs)*.9)]:.2f}")

if __name__ == "__main__":
    main()
