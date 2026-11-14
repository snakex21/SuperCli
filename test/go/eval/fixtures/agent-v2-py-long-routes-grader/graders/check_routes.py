import os, sys
problems = []
def norm(s):
    return "\n".join(l.rstrip() for l in s.replace("\r\n", "\n").strip().split("\n"))
want = norm(open("graders/ROUTES.expected.md", encoding="utf-8").read())
try:
    got = norm(open("ROUTES.md", encoding="utf-8").read())
except OSError:
    got = ""
    problems.append("ROUTES.md missing")
if got and got != want:
    wl, gl = want.split("\n"), got.split("\n")
    bad = sum(1 for i in range(max(len(wl), len(gl))) if (wl[i] if i < len(wl) else None) != (gl[i] if i < len(gl) else None))
    problems.append("ROUTES.md differs in %d of %d lines" % (bad, len(wl)))
    for i in range(max(len(wl), len(gl))):
        a = wl[i] if i < len(wl) else None
        b = gl[i] if i < len(gl) else None
        if a != b:
            problems.append("  first diff at line %d: want %r got %r" % (i + 1, a, b))
            break
for name in sorted(os.listdir("graders/expected")):
    rel = "handlers/" + name[:-4]
    want_h = open(os.path.join("graders/expected", name), encoding="utf-8").read().replace("\r\n", "\n")
    got_h = open(rel, encoding="utf-8").read().replace("\r\n", "\n")
    if want_h != got_h:
        problems.append(rel + ": wrong content (typo not fixed, or other changes)")
for p in problems:
    print(p)
sys.exit(1 if problems else 0)
