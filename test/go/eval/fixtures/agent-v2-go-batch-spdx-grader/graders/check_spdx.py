import os, sys
bad = []
exp_root = os.path.join("graders", "expected")
seen = set()
for root, _, files in os.walk(exp_root):
    for name in files:
        rel = os.path.relpath(os.path.join(root, name), exp_root)[:-4].replace(os.sep, "/")
        seen.add(rel)
        want = open(os.path.join(root, name), encoding="utf-8").read().replace("\r\n", "\n")
        try:
            got = open(rel, encoding="utf-8").read().replace("\r\n", "\n")
        except OSError:
            bad.append(rel + ": missing")
            continue
        if got != want:
            first = got.splitlines()[:3]
            bad.append("%s: unexpected content, first lines %r" % (rel, first))
for root, _, files in os.walk("pkg"):
    for name in files:
        rel = os.path.join(root, name).replace(os.sep, "/")
        if rel.endswith(".go") and rel not in seen:
            bad.append(rel + ": unexpected new file")
total = len(seen)
print("files checked:", total, "problems:", len(bad))
for b in bad[:40]:
    print(" ", b)
if bad:
    print("EVAL_SCORE=%.3f" % (max(0.0, 1 - len(bad) / float(total))))
    sys.exit(1)
