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
from datetime import date
from decimal import ROUND_HALF_UP, Decimal

sys.path.insert(0, os.getcwd())
from odsetki.kalkulator import do_zaplaty, nalicz_odsetki  # noqa: E402

def want(kwota, stopa, termin, zaplata):
    dni = max((zaplata - termin).days, 0)
    return (Decimal(kwota) * Decimal(stopa) * dni / 365).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)

cases = [("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)),
         ("5000.00", "0.1150", date(2024, 2, 1), date(2024, 3, 1)),  # leap year: still 365
         ("1234.56", "0.0825", date(2025, 12, 15), date(2026, 2, 14)),
         ("99.99", "0.1125", date(2025, 6, 1), date(2025, 6, 2))]
for c in cases:
    got = nalicz_odsetki(*c)
    check("nalicz_odsetki%s" % (c[:2],), got == want(*c), (str(got), str(want(*c))))
check("do_zaplaty", do_zaplaty("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)) == Decimal("10092.47"))
finish()
