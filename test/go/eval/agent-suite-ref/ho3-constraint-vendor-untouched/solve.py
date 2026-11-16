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
 "src/urls.js": "aW1wb3J0IHZlbmRvclNsdWdpZnkgZnJvbSAiLi4vdmVuZG9yL3NsdWdpZnkvaW5kZXguanMiOwoKLy8gVGhlIHZlbmRvcmVkIHNsdWdpZnkgZHJvcHMgbGV0dGVycyB3aXRob3V0IGEgVW5pY29kZSBkZWNvbXBvc2l0aW9uICjFgiwgxYEpOgovLyB0cmFuc2xpdGVyYXRlIHRoZW0gYmVmb3JlIHNsdWdpZnlpbmcuCmZ1bmN0aW9uIHNsdWdpZnkodGV4dCkgewogIHJldHVybiB2ZW5kb3JTbHVnaWZ5KFN0cmluZyh0ZXh0KS5yZXBsYWNlKC/Fgi9nLCAibCIpLnJlcGxhY2UoL8WBL2csICJMIikpOwp9CgovKiogUHVibGljIFVSTCBvZiBhbiBhcnRpY2xlOiAvYXJ0eWt1bHkvPGlkPi08c2x1Zz4uICovCmV4cG9ydCBmdW5jdGlvbiBhcnRpY2xlVXJsKGFydGljbGUpIHsKICByZXR1cm4gYC9hcnR5a3VseS8ke2FydGljbGUuaWR9LSR7c2x1Z2lmeShhcnRpY2xlLnRpdGxlKX1gOwp9CgovKiogUHVibGljIFVSTCBvZiBhIHRhZyBsaXN0aW5nLiAqLwpleHBvcnQgZnVuY3Rpb24gdGFnVXJsKHRhZykgewogIHJldHVybiBgL3RhZy8ke3NsdWdpZnkodGFnKX1gOwp9CgovKiogQ2Fub25pY2FsIGF1dGhvciBwYWdlOyBhdXRob3JzIGFyZSBhZGRyZXNzZWQgYnkgdGhlaXIgbG9naW4gb25seS4gKi8KZXhwb3J0IGZ1bmN0aW9uIGF1dGhvclVybChsb2dpbikgewogIHJldHVybiBgL2F1dG9yLyR7ZW5jb2RlVVJJQ29tcG9uZW50KGxvZ2luKX1gOwp9Cg=="
}
DELETE = []
ANSWER = "The vendored slugify strips diacritics via NFD, but \u0142/\u0141 have no decomposition, so they were dropped. Fixed in src/urls.js only: a local wrapper transliterates \u0142/\u0141 to l/L before calling the vendored slugify, used by articleUrl and tagUrl. vendor/ and the tests are untouched; tests pass."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
