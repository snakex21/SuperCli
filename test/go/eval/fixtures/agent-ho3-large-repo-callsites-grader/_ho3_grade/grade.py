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
EXPECTED = [
 "billing/tenancy.py:20",
 "services/accounts/handlers/badge_2.py:29",
 "services/accounts/handlers/badge_2.py:35",
 "services/accounts/handlers/badge_2.py:42",
 "services/accounts/models/token_1.py:31",
 "services/accounts/models/token_1.py:38",
 "services/accounts/views/bucket_3.py:20",
 "services/audit/views/route_2.py:17",
 "services/audit/views/route_2.py:23",
 "services/audit/views/route_2.py:30",
 "services/catalog/util/folio_1.py:32",
 "services/catalog/util/folio_1.py:38",
 "services/exports/util/batch_4.py:37",
 "services/exports/util/batch_4.py:43",
 "services/exports/util/batch_4.py:50",
 "services/exports/views/window_5.py:21",
 "services/imports/handlers/bucket_3.py:32",
 "services/imports/views/ticket_4.py:32",
 "services/invoices/handlers/route_2.py:31",
 "services/invoices/handlers/route_2.py:37",
 "services/ledger/views/draft_3.py:18",
 "services/ledger/views/draft_3.py:25",
 "services/ledger/views/draft_3.py:32",
 "services/loyalty/models/cursor_1.py:24",
 "services/payments/models/manifest_4.py:19",
 "services/payments/models/manifest_4.py:26",
 "services/payments/models/manifest_4.py:32",
 "services/pricing/handlers/bucket_1.py:18",
 "services/pricing/handlers/bucket_1.py:25",
 "services/pricing/handlers/bucket_1.py:32",
 "services/pricing/models/draft_3.py:28",
 "services/returns/handlers/quota_1.py:27",
 "services/search/handlers/bucket_5.py:32",
 "services/shipping/jobs/batch_1.py:31",
 "services/shipping/jobs/batch_1.py:37",
 "services/shipping/jobs/batch_1.py:44",
 "tests/test_tenancy.py:16",
 "tests/test_tenancy.py:20"
]
import re

if not check("callsites.txt exists", os.path.exists("callsites.txt")):
    finish()
with open("callsites.txt", encoding="utf-8-sig") as f:
    raw = f.read()
got = []
for line in raw.splitlines():
    line = line.strip().replace("\\", "/")
    if not line or line.startswith("#"):
        continue
    line = re.sub(r"^\./", "", line)
    m = re.match(r"^(.+?\.py):(\d+)\b", line)
    got.append("%s:%d" % (m.group(1), int(m.group(2))) if m else line)
want, have = set(EXPECTED), set(got)
missing, extra = sorted(want - have), sorted(have - want)
check("no missing call sites (%d expected)" % len(want), not missing, "%d missing, e.g. %s" % (len(missing), missing[:4]))
check("no false positives", not extra, "%d extra, e.g. %s" % (len(extra), extra[:4]))
check("one entry per line, no duplicates", len(got) == len(have), len(got) - len(have))
key = lambda s: (s.rsplit(":", 1)[0], int(s.rsplit(":", 1)[1]) if s.rsplit(":", 1)[-1].isdigit() else 0)
check("sorted by path then line", got == sorted(got, key=key))
if missing or extra:
    tp = len(want & have)
    print("EVAL_SCORE=%.3f" % (tp / float(len(want | have))))
    sys.exit(1)
finish()
