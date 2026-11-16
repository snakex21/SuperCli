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
SUMMARY = {
 "customers": [
  {
   "klient": "Apteka \"Pod Or\u0142em\"",
   "suma_gr": 11548730,
   "transakcje": 7,
   "zwroty_gr": 3998
  },
  {
   "klient": "Zielony Gaj",
   "suma_gr": 9654304,
   "transakcje": 6,
   "zwroty_gr": 5000
  },
  {
   "klient": "Hurtownia \u0141\u0105ka sp. z o.o.",
   "suma_gr": 7231890,
   "transakcje": 7,
   "zwroty_gr": 0
  },
  {
   "klient": "ZIELONY GAJ",
   "suma_gr": 6749219,
   "transakcje": 5,
   "zwroty_gr": 0
  },
  {
   "klient": "B\u0105k Tomasz",
   "suma_gr": 4397261,
   "transakcje": 3,
   "zwroty_gr": 0
  },
  {
   "klient": "Kowalski, Jan",
   "suma_gr": 4308091,
   "transakcje": 5,
   "zwroty_gr": 0
  },
  {
   "klient": "\u0106ma i Syn",
   "suma_gr": 2350056,
   "transakcje": 2,
   "zwroty_gr": 0
  },
  {
   "klient": "\u0179r\u00f3d\u0142o S.A.",
   "suma_gr": 76923,
   "transakcje": 5,
   "zwroty_gr": 1999
  }
 ],
 "pominiete": {
  "anulowane": 9,
  "duplikaty": 6,
  "oczekujace": 5
 },
 "razem_gr": 46316474
}
MONTHLY = [
 [
  "miesiac",
  "klient",
  "suma_pln"
 ],
 [
  "2026-01",
  "Apteka \"Pod Or\u0142em\"",
  "107741.07"
 ],
 [
  "2026-01",
  "B\u0105k Tomasz",
  "20125.55"
 ],
 [
  "2026-01",
  "Hurtownia \u0141\u0105ka sp. z o.o.",
  "44863.82"
 ],
 [
  "2026-01",
  "Kowalski, Jan",
  "17881.48"
 ],
 [
  "2026-01",
  "Zielony Gaj",
  "1284.82"
 ],
 [
  "2026-01",
  "\u0106ma i Syn",
  "22943.50"
 ],
 [
  "2026-01",
  "\u0179r\u00f3d\u0142o S.A.",
  "484.15"
 ],
 [
  "2026-02",
  "Apteka \"Pod Or\u0142em\"",
  "306.14"
 ],
 [
  "2026-02",
  "B\u0105k Tomasz",
  "998.64"
 ],
 [
  "2026-02",
  "Hurtownia \u0141\u0105ka sp. z o.o.",
  "3258.47"
 ],
 [
  "2026-02",
  "Kowalski, Jan",
  "370.09"
 ],
 [
  "2026-02",
  "ZIELONY GAJ",
  "67492.19"
 ],
 [
  "2026-02",
  "Zielony Gaj",
  "51143.69"
 ],
 [
  "2026-02",
  "\u0106ma i Syn",
  "557.06"
 ],
 [
  "2026-02",
  "\u0179r\u00f3d\u0142o S.A.",
  "215.82"
 ],
 [
  "2026-03",
  "Apteka \"Pod Or\u0142em\"",
  "7440.09"
 ],
 [
  "2026-03",
  "B\u0105k Tomasz",
  "22848.42"
 ],
 [
  "2026-03",
  "Hurtownia \u0141\u0105ka sp. z o.o.",
  "24196.61"
 ],
 [
  "2026-03",
  "Kowalski, Jan",
  "24829.34"
 ],
 [
  "2026-03",
  "Zielony Gaj",
  "44114.53"
 ],
 [
  "2026-03",
  "\u0179r\u00f3d\u0142o S.A.",
  "69.26"
 ]
]
import csv
import io
import json

try:
    with open("output/summary.json", encoding="utf-8-sig") as f:
        got = json.load(f)
    ok = True
except Exception as e:  # noqa: BLE001
    check("summary.json is valid JSON", False, e)
    got, ok = {}, False
if ok:
    check("razem_gr", got.get("razem_gr") == SUMMARY["razem_gr"], (got.get("razem_gr"), SUMMARY["razem_gr"]))
    check("pominiete", got.get("pominiete") == SUMMARY["pominiete"], (got.get("pominiete"), SUMMARY["pominiete"]))
    gc = got.get("customers")
    check("customers (values and order)", gc == SUMMARY["customers"],
          next(("#%d got %s want %s" % (i, a, b) for i, (a, b) in enumerate(zip(gc or [], SUMMARY["customers"])) if a != b),
               "length %s vs %d" % (len(gc) if isinstance(gc, list) else gc, len(SUMMARY["customers"]))))
    check("no extra top-level keys", set(got) == set(SUMMARY), sorted(set(got) ^ set(SUMMARY)))
try:
    with open("output/miesiecznie.csv", encoding="utf-8-sig", newline="") as f:
        rows = [r for r in csv.reader(f) if r]
    check("miesiecznie.csv header", rows[:1] == MONTHLY[:1], rows[:1])
    bad = [(i, a, b) for i, (a, b) in enumerate(zip(rows, MONTHLY)) if a != b]
    check("miesiecznie.csv rows", not bad and len(rows) == len(MONTHLY),
          bad[:2] if bad else "rows %d want %d" % (len(rows), len(MONTHLY)))
except Exception as e:  # noqa: BLE001
    check("miesiecznie.csv readable", False, e)
finish()
