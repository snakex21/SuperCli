import os, sys

def norm(s):
    return "\n".join(l.rstrip() for l in s.replace("\r\n", "\n").strip().split("\n"))

base = "graders/expected"
problems = []
total = 0
for root, _, names in os.walk(base):
    for n in names:
        exp_path = os.path.join(root, n)
        rel = os.path.relpath(exp_path, base).replace("\\", "/")[:-4]
        total += 1
        want = norm(open(exp_path, encoding="utf-8").read())
        try:
            got = norm(open(rel, encoding="utf-8").read())
        except OSError:
            problems.append(rel + ": missing")
            continue
        if got != want:
            wl, gl = want.split("\n"), got.split("\n")
            for i in range(max(len(wl), len(gl))):
                a = wl[i] if i < len(wl) else None
                b = gl[i] if i < len(gl) else None
                if a != b:
                    problems.append("%s: first difference at line %d: want %r got %r" % (rel, i + 1, a, b))
                    break
for p in problems:
    print(p)
if problems:
    print("EVAL_SCORE=%.2f" % (1 - len(problems) / float(total)))
sys.exit(1 if problems else 0)
