import os
import sys

sys.dont_write_bytecode = True
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass

_results = []


def check(name, ok, detail=""):
    _results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + (("  -- " + str(detail)) if (detail and not ok) else ""))
    return bool(ok)


def finish():
    passed = sum(1 for _, ok in _results if ok)
    total = len(_results) or 1
    if passed == total:
        print("all %d checks passed" % total)
        sys.exit(0)
    print("EVAL_SCORE=%.3f" % (passed / total))
    sys.exit(1)


def lp(path):
    """Long-path safe absolute path on Windows."""
    p = os.path.abspath(path)
    if os.name == "nt" and not p.startswith("\\\\?\\"):
        p = "\\\\?\\" + p
    return p


def read_bytes(path):
    with open(lp(path), "rb") as f:
        return f.read()


def norm_text(b):
    if isinstance(b, bytes):
        b = b.decode("utf-8-sig", "replace")
    lines = b.replace("\r\n", "\n").split("\n")
    return "\n".join(l.rstrip(" \t\r") for l in lines).rstrip("\n")
import subprocess


def git(*args):
    r = subprocess.run(["git"] + list(args), capture_output=True, text=True, encoding="utf-8", errors="replace")
    return r.returncode, r.stdout.strip(), r.stderr.strip()


def rev(ref):
    code, out, _ = git("rev-parse", "-q", "--verify", ref + "^{commit}")
    return out if code == 0 else None


def branches():
    return sorted(git("for-each-ref", "--format=%(refname:short)", "refs/heads")[1].split())
EXP = {
 "changelog": [
  "- Volume discounts: 5% from 50 units, 10% from 100 units per line.",
  "- `order_total` takes `vat_rate`; VAT is computed per line."
 ],
 "feature": "4b9e0eb5d22b5f3b478a31ff5a20580f2f374007",
 "main": "cf0af712ec540fbcae596fa18d29306eb71d8b57"
}
import random
from decimal import ROUND_HALF_UP, Decimal

check("main untouched", rev("refs/heads/main") == EXP["main"], rev("refs/heads/main"))
check("feature/volume-discounts untouched", rev("refs/heads/feature/volume-discounts") == EXP["feature"])
rel = rev("refs/heads/release/1.4")
check("release/1.4 exists", rel is not None, branches())
code, head, _ = git("symbolic-ref", "-q", "HEAD")
check("release/1.4 checked out", head == "refs/heads/release/1.4", head)
if rel:
    code, out, _ = git("rev-list", "--parents", "release/1.4", "^" + EXP["main"], "^" + EXP["feature"])
    merges = [l.split()[1:] for l in out.splitlines() if len(l.split()) > 2]
    check("a merge commit joins main and the feature branch",
          any(sorted(p) == sorted([EXP["main"], EXP["feature"]]) for p in merges), merges)
    check("no extra merges / at most 3 new commits", len(out.splitlines()) <= 3 and len(merges) == 1, out)
    code, out, _ = git("grep", "-n", "-E", r"^(<<<<<<<|>>>>>>>|=======$)", "release/1.4", "--", ".")
    check("no conflict markers committed", code == 1 and not out, out[:300])
code, out, _ = git("status", "--porcelain", "--untracked-files=no")
check("working tree clean", not out, out[:300])
for p in ("MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD"):
    check("no operation in progress: " + p, not os.path.exists(os.path.join(".git", p)))

with open("CHANGELOG.md", encoding="utf-8") as f:
    cl = f.read()
for line in EXP["changelog"]:
    check("changelog keeps: " + line[:30], line in cl)
check("changelog entries under Unreleased",
      all(cl.find(l) > cl.find("## Unreleased") and cl.find(l) < cl.find("## 1.3.0") for l in EXP["changelog"]))

sys.path.insert(0, os.getcwd())
try:
    from pricing.core import order_total
except Exception as e:  # noqa: BLE001
    check("pricing.core imports", False, e)
    finish()
CENT = Decimal("0.01")
m = lambda x: Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)

def want(lines, vat):
    total = Decimal("0")
    for p, q in lines:
        rate = Decimal("0.10") if q >= 100 else Decimal("0.05") if q >= 50 else Decimal("0")
        net = m(Decimal(p) * q * (1 - rate))
        total += m(net * (1 + Decimal(vat)))
    return m(total)

rng = random.Random(14)
cases = [([("19.99", 120), ("5.00", 60), ("3.33", 7)], "0.23"), ([("2.00", 120)], "0"), ([("0.05", 1)], "0.08")]
for _ in range(40):
    lines = [("%d.%02d" % (rng.randint(0, 80), rng.randint(0, 99)), rng.choice([1, 3, 49, 50, 51, 99, 100, 150]))
             for _ in range(rng.randint(1, 4))]
    cases.append((lines, rng.choice(["0", "0.05", "0.08", "0.23"])))
bad = []
for lines, vat in cases:
    try:
        got = order_total(lines, vat_rate=Decimal(vat))
    except Exception as e:  # noqa: BLE001
        got = repr(e)
    if got != want(lines, vat):
        bad.append((lines, vat, str(got), str(want(lines, vat))))
check("discount then VAT, both per line", not bad, bad[:2])
check("default vat_rate still 0", order_total([("2.00", 120)]) == Decimal("216.00"))
finish()
