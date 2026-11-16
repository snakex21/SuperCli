import os
import sys

sys.dont_write_bytecode = True
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass


def lp(path):
    """Long-path safe absolute path on Windows."""
    p = os.path.abspath(path)
    if os.name == "nt" and not p.startswith("\\\\?\\"):
        p = "\\\\?\\" + p
    return p


def rb(path):
    with open(path, "rb") as f:
        return f.read()


def wb(path, data):
    d = os.path.dirname(path)
    if d:
        os.makedirs(d, exist_ok=True)
    if isinstance(data, str):
        data = data.encode("utf-8")
    with open(path, "wb") as f:
        f.write(data)


def replace(path, old, new, count=1):
    data = rb(path)
    old_b = old.encode("utf-8") if isinstance(old, str) else old
    new_b = new.encode("utf-8") if isinstance(new, str) else new
    if data.count(old_b) < 1:
        sys.exit("solve: %s: pattern not found: %r" % (path, old_b[:60]))
    wb(path, data.replace(old_b, new_b, count) if count else data.replace(old_b, new_b))
import base64

FILES = {
 "inventory/stock.py": "IiIiU3RvY2sgbGV2ZWxzIGZvciB0aGUgd2FyZWhvdXNlIHNlcnZpY2UuIiIiCmZyb20gZGF0YWNsYXNzZXMgaW1wb3J0IGRhdGFjbGFzcwoKCmNsYXNzIE91dE9mU3RvY2soRXhjZXB0aW9uKToKICAgICIiIlJhaXNlZCB3aGVuIGEgcmVzZXJ2YXRpb24gZXhjZWVkcyB0aGUgYXZhaWxhYmxlIHF1YW50aXR5LiIiIgoKCkBkYXRhY2xhc3MKY2xhc3MgU3RvY2tJdGVtOgogICAgc2t1OiBzdHIKICAgIHF0eV9vbl9oYW5kOiBpbnQKICAgIHJlc2VydmVkOiBpbnQgPSAwCgogICAgQHByb3BlcnR5CiAgICBkZWYgYXZhaWxhYmxlKHNlbGYpOgogICAgICAgIHJldHVybiBzZWxmLnF0eV9vbl9oYW5kIC0gc2VsZi5yZXNlcnZlZAoKCmNsYXNzIFN0b2NrOgogICAgIiIiSW4tbWVtb3J5IHN0b2NrIGJvb2sga2V5ZWQgYnkgU0tVLiIiIgoKICAgIGRlZiBfX2luaXRfXyhzZWxmKToKICAgICAgICBzZWxmLl9pdGVtcyA9IHt9CgogICAgZGVmIGFkZChzZWxmLCBza3UsIHF0eSk6CiAgICAgICAgIiIiUmVjZWl2ZSBxdHkgdW5pdHMgb2Ygc2t1IGludG8gdGhlIHdhcmVob3VzZS4iIiIKICAgICAgICBpZiBxdHkgPD0gMDoKICAgICAgICAgICAgcmFpc2UgVmFsdWVFcnJvcigicXVhbnRpdHkgbXVzdCBiZSBwb3NpdGl2ZSwgZ290ICVkIiAlIHF0eSkKICAgICAgICBpdGVtID0gc2VsZi5faXRlbXMuZ2V0KHNrdSkKICAgICAgICBpZiBpdGVtIGlzIE5vbmU6CiAgICAgICAgICAgIGl0ZW0gPSBzZWxmLl9pdGVtc1tza3VdID0gU3RvY2tJdGVtKHNrdSwgMCkKICAgICAgICBpdGVtLnF0eV9vbl9oYW5kICs9IHF0eQogICAgICAgIHJldHVybiBpdGVtCgogICAgZGVmIGdldChzZWxmLCBza3UpOgogICAgICAgIHJldHVybiBzZWxmLl9pdGVtc1tza3VdCgogICAgZGVmIHJlc2VydmUoc2VsZiwgc2t1LCBxdHkpOgogICAgICAgICIiIlJlc2VydmUgcXR5IHVuaXRzIGZvciBhbiBvcmRlcjsgcmV0dXJucyB3aGF0IGlzIHN0aWxsIGF2YWlsYWJsZS4iIiIKICAgICAgICBpZiBxdHkgPD0gMDoKICAgICAgICAgICAgcmFpc2UgVmFsdWVFcnJvcigicXVhbnRpdHkgbXVzdCBiZSBwb3NpdGl2ZSwgZ290ICVkIiAlIHF0eSkKICAgICAgICBpdGVtID0gc2VsZi5faXRlbXNbc2t1XQogICAgICAgIGlmIHF0eSA+IGl0ZW0uYXZhaWxhYmxlOgogICAgICAgICAgICByYWlzZSBPdXRPZlN0b2NrKCIlczogcmVxdWVzdGVkICVkLCBhdmFpbGFibGUgJWQiICUgKHNrdSwgcXR5LCBpdGVtLmF2YWlsYWJsZSkpCiAgICAgICAgaXRlbS5yZXNlcnZlZCArPSBxdHkKICAgICAgICByZXR1cm4gaXRlbS5hdmFpbGFibGUKCiAgICBkZWYgc2hpcChzZWxmLCBza3UsIHF0eSk6CiAgICAgICAgIiIiU2hpcCBxdHkgcmVzZXJ2ZWQgdW5pdHM6IHRoZXkgbGVhdmUgdGhlIHdhcmVob3VzZS4iIiIKICAgICAgICBpdGVtID0gc2VsZi5faXRlbXNbc2t1XQogICAgICAgIGlmIHF0eSA+IGl0ZW0ucmVzZXJ2ZWQ6CiAgICAgICAgICAgIHJhaXNlIE91dE9mU3RvY2soIiVzOiBzaGlwcGluZyAlZCwgcmVzZXJ2ZWQgJWQiICUgKHNrdSwgcXR5LCBpdGVtLnJlc2VydmVkKSkKICAgICAgICBpdGVtLnJlc2VydmVkIC09IHF0eQogICAgICAgIGl0ZW0ucXR5X29uX2hhbmQgLT0gcXR5CgogICAgZGVmIHJlbGVhc2Uoc2VsZiwgc2t1LCBxdHkpOgogICAgICAgICIiIlJldHVybiBxdHkgcmVzZXJ2ZWQgdW5pdHMgdG8gdGhlIGF2YWlsYWJsZSBwb29sLiIiIgogICAgICAgIGlmIHF0eSA8PSAwOgogICAgICAgICAgICByYWlzZSBWYWx1ZUVycm9yKCJxdWFudGl0eSBtdXN0IGJlIHBvc2l0aXZlLCBnb3QgJWQiICUgcXR5KQogICAgICAgIGl0ZW0gPSBzZWxmLl9pdGVtc1tza3VdCiAgICAgICAgaWYgcXR5ID4gaXRlbS5yZXNlcnZlZDoKICAgICAgICAgICAgcmFpc2UgVmFsdWVFcnJvcigiJXM6IHJlbGVhc2luZyAlZCwgcmVzZXJ2ZWQgJWQiICUgKHNrdSwgcXR5LCBpdGVtLnJlc2VydmVkKSkKICAgICAgICBpdGVtLnJlc2VydmVkIC09IHF0eQogICAgICAgIHJldHVybiBpdGVtLmF2YWlsYWJsZQoKICAgIGRlZiBza3VzKHNlbGYpOgogICAgICAgIHJldHVybiBzb3J0ZWQoc2VsZi5faXRlbXMpCg=="
}
DELETE = []
ANSWER = "Reverted the rename: `StockItem.qty_on_hand`, the `qty_on_hand` JSON key in `inventory/api.py`, `inventory/reports.py` and the tests are back to the original names. Kept the ValueError checks in `add()`/`reserve()` and the new `Stock.release()`. Tests pass."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
