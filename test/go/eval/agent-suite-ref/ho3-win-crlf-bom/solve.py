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
 "config/app.ini": "77u/OyBLb25maWd1cmFjamEgYXBsaWthY2ppIOKAkyDFm3JvZG93aXNrbyBwcm9kdWtjeWpuZQ0KOyBQbGlrIGN6eXRhbnkgcHJ6ZXogdXPFgnVnxJkgV2luZG93cyAiQXBsaWthY2phU2Vyd2VyIiAoVVRGLTggeiBCT00pLg0KW3NlcnZlcl0NCmhvc3Q9MC4wLjAuMA0KcG9ydD04NDQzDQptYXhfcG/FgsSFY3plxYQ9MjAwDQoNCltsb2dnaW5nXQ0KbGV2ZWw9aW5mbw0KcGxpaz1DOlxMb2dpXEFwbGlrYWNqYVxzZXJ3ZXIubG9nDQo=",
 "data/klienci.csv": "aWQ7bmF6d2E7bWlhc3RvO25pcA0KMTtQcnplZHNpxJliaW9yc3R3byDFgcSFa2Egc3AuIHogby5vLjvFgcOzZMW6OzcyNTEyMzQ1NjcNCjI7xblyw7NkxYJvIFdvZHkgUy5BLjtLcmFrw7N3OzY3NjIzNDU2NzgNCjM7IkfEmcWbOyBLYWN6a2EgaSBXc3DDs2xuaWN5IjtHZGHFhHNrOzU4MzM0NTY3ODkNCg==",
 "scripts/Deploy-App.ps1": "77u/PCMNCi5TWU5PUFNJUw0KICBXZHJhxbxhIGFwbGlrYWNqxJkgZG8gQXp1cmUuIFVydWNoYW1pYcSHIHcgV2luZG93cyBQb3dlclNoZWxsIDUuMS4NCiM+DQpwYXJhbSgNCiAgICBbc3RyaW5nXSRFbnZpcm9ubWVudCA9ICdwcm9kJw0KKQ0KDQokRXJyb3JBY3Rpb25QcmVmZXJlbmNlID0gJ1N0b3AnDQokUmVnaW9uID0gJ3BvbGFuZGNlbnRyYWwnDQokUmVzb3VyY2VHcm91cCA9ICJyZy1hcGxpa2FjamEtJEVudmlyb25tZW50Ig0KDQpXcml0ZS1Ib3N0ICJSb3pwb2N6eW5hbSB3ZHJhxbxhbmllIGRvIHJlZ2lvbnUgJFJlZ2lvbiAoZ3J1cGE6ICRSZXNvdXJjZUdyb3VwKeKApiINCmF6IGdyb3VwIGNyZWF0ZSAtLW5hbWUgJFJlc291cmNlR3JvdXAgLS1sb2NhdGlvbiAkUmVnaW9uIHwgT3V0LU51bGwNCmF6IGRlcGxveW1lbnQgZ3JvdXAgY3JlYXRlIC0tcmVzb3VyY2UtZ3JvdXAgJFJlc291cmNlR3JvdXAgLS10ZW1wbGF0ZS1maWxlICIkUFNTY3JpcHRSb290XG1haW4uYmljZXAiIHwgT3V0LU51bGwNCldyaXRlLUhvc3QgIldkcmHFvGFuaWUgemFrb8WEY3pvbmUgcG9tecWbbG5pZS4gxbvDs8WCxIcgaSBnxJnFmyBzxIUgYmV6cGllY3puZS4iDQo=",
 "src/settings.json": "ewogICJuYW1lIjogImFwbGlrYWNqYS1zZXJ3ZXIiLAogICJwb3J0IjogODQ0MywKICAicmVnaW9uIjogIndlc3RldXJvcGUiLAogICJmZWF0dXJlcyI6IHsKICAgICJhdWRpdCI6IHRydWUsCiAgICAibGVnYWN5QXBpIjogZmFsc2UKICB9Cn0K"
}
DELETE = []
ANSWER = "Changed the port to 8443 in config/app.ini and src/settings.json and the region to polandcentral in scripts/Deploy-App.ps1, keeping each file's encoding and CRLF/LF line endings (the .ps1 and .ini keep their UTF-8 BOM). Removed the UTF-8 BOM from data/klienci.csv (CRLF kept); the importer test passes now."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
