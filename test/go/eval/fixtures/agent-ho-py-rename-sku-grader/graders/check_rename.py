import json, os, re, sys
exp = json.load(open("graders/rename_expect.json", encoding="utf-8"))
old = re.compile(r"\bnormalize_sku\b")
problems = []
for root, dirs, files in os.walk("."):
    dirs[:] = [x for x in dirs if x not in ("graders", "__pycache__", ".git", ".supercli")]
    for n in files:
        rel = os.path.relpath(os.path.join(root, n), ".").replace("\\", "/")
        if rel.endswith(".pyc"):
            continue
        try:
            text = open(rel, encoding="utf-8").read()
        except (OSError, UnicodeDecodeError):
            continue
        if rel == "CHANGELOG.md" or rel.startswith("tests/test_zz_hidden"):
            continue
        hits = old.findall(text)
        if hits:
            problems.append("%s: still mentions normalize_sku %d time(s)" % (rel, len(hits)))
        if rel not in exp["files"]:
            continue
        want = exp["decoys"].get(rel, {})
        for name in ("denormalize_sku", "normalize_sku_prefix", "normalize_skus"):
            got = len(re.findall(r"\b%s\b" % name, text))
            if got != want.get(name, 0):
                problems.append("%s: %s occurs %d time(s), expected %d (unrelated identifier changed)" % (rel, name, got, want.get(name, 0)))
for rel in exp["changed"]:
    try:
        if "canonical_sku" not in open(rel, encoding="utf-8").read():
            problems.append(rel + ": canonical_sku missing")
    except OSError:
        problems.append(rel + ": missing")
for p in problems:
    print(p)
if problems:
    print("EVAL_SCORE=%.2f" % max(0.0, 1 - len(problems) / 20.0))
sys.exit(1 if problems else 0)
