#!/usr/bin/env python3
"""Repair examples: a student's real failed answer, zshai's feedback, and the teacher's checked answer.

zshai sends an answer that fails its checks back to the model ("That reply failed zshai's checks: …").
zshai-mini was never trained on that turn, so it answered the feedback with unrelated recipes. This
builds two-turn examples from a student run (run_teacher.py with the student as --model and --no-judge):

    [system, user: context + request, assistant: the student's failed answer,
     user: zshai's feedback, assistant: the teacher's verified answer for the same request]

    python3 datagen/gen_repairs.py OUT.jsonl --student s.jsonl s.err [s2.jsonl s2.err …] --teacher ex-mac.jsonl ex-linux.jsonl …

Only the first failed round is used (later rounds repeat themselves). stdlib only.
"""
import argparse, json, re

def load(files):
    for f in files:
        for l in open(f):
            if l.startswith("{"):
                yield json.loads(l)

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("out")
    ap.add_argument("--student", nargs="+", required=True)
    ap.add_argument("--teacher", nargs="+", required=True)
    a = ap.parse_args()
    teacher = {}
    for j in load(a.teacher):
        if j.get("target") and not j.get("drop"):
            teacher[(j["os"], j["seed"], j["cwd"], j["request"])] = j
    n = miss = 0
    seen = set()
    with open(a.out, "w") as out:
        for s in load(a.student):
            tr = s.get("trace") or []
            if len(tr) < 2 or not tr[1][0].startswith("That reply failed zshai's checks"):
                continue
            t = teacher.get((s["os"], s["seed"], s["cwd"], s["request"]))
            if not t:
                miss += 1
                continue
            bad, feedback = tr[0][1].strip(), tr[1][0].strip()
            # the student's failed answer may equal the target (a soft check the teacher kept): skip those
            body = lambda x: x.split("\n---\n", 1)[-1].strip()
            if body(bad) == body(t["target"]):
                continue
            key = (s["os"], tr[0][0], bad)
            if key in seen:
                continue
            seen.add(key)
            out.write(json.dumps({"os": s["os"], "seed": s["seed"], "lang": s["lang"], "cat": s.get("cat", ""),
                                  "prompt": tr[0][0], "bad": bad, "feedback": feedback, "target": t["target"],
                                  "verified": "repair"}, ensure_ascii=False) + "\n")
            n += 1
    print(f"{n} repair examples ({miss} failures without a teacher answer)")

if __name__ == "__main__":
    main()
