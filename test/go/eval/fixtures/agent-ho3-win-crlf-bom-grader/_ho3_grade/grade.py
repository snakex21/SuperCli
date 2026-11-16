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
import base64
EXPECTED = {
 "config/app.ini": "77u/OyBLb25maWd1cmFjamEgYXBsaWthY2ppIOKAkyDFm3JvZG93aXNrbyBwcm9kdWtjeWpuZQ0KOyBQbGlrIGN6eXRhbnkgcHJ6ZXogdXPFgnVnxJkgV2luZG93cyAiQXBsaWthY2phU2Vyd2VyIiAoVVRGLTggeiBCT00pLg0KW3NlcnZlcl0NCmhvc3Q9MC4wLjAuMA0KcG9ydD04NDQzDQptYXhfcG/FgsSFY3plxYQ9MjAwDQoNCltsb2dnaW5nXQ0KbGV2ZWw9aW5mbw0KcGxpaz1DOlxMb2dpXEFwbGlrYWNqYVxzZXJ3ZXIubG9nDQo=",
 "data/klienci.csv": "aWQ7bmF6d2E7bWlhc3RvO25pcA0KMTtQcnplZHNpxJliaW9yc3R3byDFgcSFa2Egc3AuIHogby5vLjvFgcOzZMW6OzcyNTEyMzQ1NjcNCjI7xblyw7NkxYJvIFdvZHkgUy5BLjtLcmFrw7N3OzY3NjIzNDU2NzgNCjM7IkfEmcWbOyBLYWN6a2EgaSBXc3DDs2xuaWN5IjtHZGHFhHNrOzU4MzM0NTY3ODkNCg==",
 "scripts/Deploy-App.ps1": "77u/PCMNCi5TWU5PUFNJUw0KICBXZHJhxbxhIGFwbGlrYWNqxJkgZG8gQXp1cmUuIFVydWNoYW1pYcSHIHcgV2luZG93cyBQb3dlclNoZWxsIDUuMS4NCiM+DQpwYXJhbSgNCiAgICBbc3RyaW5nXSRFbnZpcm9ubWVudCA9ICdwcm9kJw0KKQ0KDQokRXJyb3JBY3Rpb25QcmVmZXJlbmNlID0gJ1N0b3AnDQokUmVnaW9uID0gJ3BvbGFuZGNlbnRyYWwnDQokUmVzb3VyY2VHcm91cCA9ICJyZy1hcGxpa2FjamEtJEVudmlyb25tZW50Ig0KDQpXcml0ZS1Ib3N0ICJSb3pwb2N6eW5hbSB3ZHJhxbxhbmllIGRvIHJlZ2lvbnUgJFJlZ2lvbiAoZ3J1cGE6ICRSZXNvdXJjZUdyb3VwKeKApiINCmF6IGdyb3VwIGNyZWF0ZSAtLW5hbWUgJFJlc291cmNlR3JvdXAgLS1sb2NhdGlvbiAkUmVnaW9uIHwgT3V0LU51bGwNCmF6IGRlcGxveW1lbnQgZ3JvdXAgY3JlYXRlIC0tcmVzb3VyY2UtZ3JvdXAgJFJlc291cmNlR3JvdXAgLS10ZW1wbGF0ZS1maWxlICIkUFNTY3JpcHRSb290XG1haW4uYmljZXAiIHwgT3V0LU51bGwNCldyaXRlLUhvc3QgIldkcmHFvGFuaWUgemFrb8WEY3pvbmUgcG9tecWbbG5pZS4gxbvDs8WCxIcgaSBnxJnFmyBzxIUgYmV6cGllY3puZS4iDQo=",
 "src/settings.json": "ewogICJuYW1lIjogImFwbGlrYWNqYS1zZXJ3ZXIiLAogICJwb3J0IjogODQ0MywKICAicmVnaW9uIjogIndlc3RldXJvcGUiLAogICJmZWF0dXJlcyI6IHsKICAgICJhdWRpdCI6IHRydWUsCiAgICAibGVnYWN5QXBpIjogZmFsc2UKICB9Cn0K"
}
def describe(b):
    return "bom=%s crlf=%d lone_lf=%d len=%d" % (b.startswith(b"\xef\xbb\xbf"), b.count(b"\r\n"),
                                              b.count(b"\n") - b.count(b"\r\n"), len(b))

for rel, enc in sorted(EXPECTED.items()):
    want = base64.b64decode(enc)
    try:
        got = read_bytes(rel)
    except OSError as e:
        check(rel + " exact bytes", False, e)
        continue
    if got == want:
        check(rel + " exact bytes", True)
        continue
    # Explain the most common failure modes.
    detail = "got " + describe(got) + " want " + describe(want)
    if norm_text(got) == norm_text(want):
        detail += " (content right, encoding/line endings/BOM wrong)"
    check(rel + " exact bytes", False, detail)
finish()
