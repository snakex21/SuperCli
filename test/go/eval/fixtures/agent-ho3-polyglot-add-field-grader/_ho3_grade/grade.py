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
import json
import re

sys.path.insert(0, os.getcwd())
from analytics.events import load_event  # noqa: E402

base = {"id": "o-1", "sku": "ABC-0001", "quantity": 2, "price_cents": 1999}
try:
    e = load_event(dict(base))
    check("archived event without currency -> PLN", getattr(e, "currency", None) == "PLN", e)
except Exception as ex:  # noqa: BLE001
    check("archived event without currency -> PLN", False, ex)
try:
    check("currency kept", load_event(dict(base, currency="USD")).currency == "USD")
except Exception as ex:  # noqa: BLE001
    check("currency kept", False, ex)
for bad in ("usd", "USDX", "", 840, None):
    try:
        load_event(dict(base, currency=bad))
        check("rejects currency %r" % (bad,), False, "accepted")
    except ValueError:
        check("rejects currency %r" % (bad,), True)
    except Exception as ex:  # noqa: BLE001
        check("rejects currency %r" % (bad,), False, "raised %r, want ValueError" % ex)

with open("schema/order_event.schema.json", encoding="utf-8") as f:
    schema = json.load(f)
prop = schema.get("properties", {}).get("currency")
check("schema: currency property", isinstance(prop, dict), prop)
check("schema: currency required", "currency" in schema.get("required", []))
if isinstance(prop, dict):
    check("schema: currency is a string", prop.get("type") == "string", prop)
    pat, enum = prop.get("pattern"), prop.get("enum")
    if pat:
        ok = all(bool(re.search(pat, v)) for v in ("PLN", "EUR")) and not any(re.search(pat, v) for v in ("pln", "EURO", "EU"))
        check("schema: pattern accepts only 3 uppercase letters (JSON Schema search semantics)", ok, pat)
    else:
        check("schema: pattern or enum restricts the code", bool(enum) and "PLN" in enum and "EUR" in enum, prop)
finish()
