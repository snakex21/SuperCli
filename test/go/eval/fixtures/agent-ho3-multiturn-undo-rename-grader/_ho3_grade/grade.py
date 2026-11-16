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
import dataclasses
import re
import importlib

sys.path.insert(0, os.getcwd())
stock_mod = importlib.import_module("inventory.stock")
api = importlib.import_module("inventory.api")
Stock = stock_mod.Stock

names = [f.name for f in dataclasses.fields(stock_mod.StockItem)]
check("field qty_on_hand restored", "qty_on_hand" in names, names)
check("no on_hand field left", "on_hand" not in names, names)

s = Stock()
s.add("X", 10)
check("api key qty_on_hand", api.to_dict(s.get("X")).get("qty_on_hand") == 10, api.to_dict(s.get("X")))
check("api has no on_hand key", "on_hand" not in api.to_dict(s.get("X")))

def raises(exc, fn, *a):
    try:
        fn(*a)
    except exc:
        return True
    except Exception as e:  # wrong type
        return False
    return False

for q in (0, -3):
    check("add(%d) raises ValueError" % q, raises(ValueError, Stock().add, "Y", q))
    st = Stock(); st.add("Y", 5)
    check("reserve(%d) raises ValueError" % q, raises(ValueError, st.reserve, "Y", q))
    check("reserve(%d) left stock untouched" % q, st.get("Y").reserved == 0 and st.get("Y").qty_on_hand == 5)

st = Stock(); st.add("Z", 10); st.reserve("Z", 6)
check("release exists", hasattr(st, "release"))
if hasattr(st, "release"):
    st.release("Z", 4)
    check("release returns units", st.get("Z").reserved == 2 and st.get("Z").available == 8,
          (st.get("Z").reserved, st.get("Z").available))
    check("release more than reserved raises ValueError", raises(ValueError, st.release, "Z", 3))
    check("failed release changed nothing", st.get("Z").reserved == 2)
    check("release(0) raises ValueError", raises(ValueError, st.release, "Z", 0))
st = Stock(); st.add("Q", 4)
check("valid add/reserve still work", st.reserve("Q", 4) == 0)

# The rename must be gone everywhere (qty_on_hand does not match \bon_hand\b).
leftovers = []
for base in ("inventory", "tests"):
    for root, _, files in os.walk(base):
        for n in files:
            if n.endswith(".py"):
                p = os.path.join(root, n)
                with open(p, encoding="utf-8") as f:
                    for i, line in enumerate(f, 1):
                        if re.search(r"\bon_hand\b", line):
                            leftovers.append("%s:%d" % (p, i))
check("no on_hand identifier left", not leftovers, leftovers[:5])

# Files the rename touched and nothing else needed must be back to the original.
for rel, exp in (("inventory/reports.py", "reports.py"), ("inventory/api.py", "api.py")):
    check(rel + " restored to original", norm_text(read_bytes(rel)) == norm_text(read_bytes(os.path.join("_ho3_grade", "expected", exp))))
cur = norm_text(read_bytes("tests/test_stock.py"))
orig = norm_text(read_bytes("_ho3_grade/expected/test_stock.py"))
check("original tests kept (rename reverted in tests)", all(l in cur.split("\n") for l in orig.split("\n") if l.strip()))
finish()
