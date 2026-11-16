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
 "durations/parse.go": "Ly8gUGFja2FnZSBkdXJhdGlvbnMgcGFyc2VzIGh1bWFuLWZyaWVuZGx5IGR1cmF0aW9ucyBzdWNoIGFzICIxaDMwbSIgb3IgIjQ1cyIKLy8gdXNlZCBpbiBkb2NnZW4ncyBjb25maWd1cmF0aW9uIGZpbGVzLgpwYWNrYWdlIGR1cmF0aW9ucwoKaW1wb3J0ICgKCSJmbXQiCgkic3RyY29udiIKCSJzdHJpbmdzIgoJInRpbWUiCikKCnZhciB1bml0cyA9IG1hcFtieXRlXXRpbWUuRHVyYXRpb257Cgkndyc6IDcgKiAyNCAqIHRpbWUuSG91ciwKCSdkJzogMjQgKiB0aW1lLkhvdXIsCgknaCc6IHRpbWUuSG91ciwKCSdtJzogdGltZS5NaW51dGUsCgkncyc6IHRpbWUuU2Vjb25kLAp9CgovLyBQYXJzZUh1bWFuIHBhcnNlcyBhIHNlcXVlbmNlIG9mIDxpbnRlZ2VyPjx1bml0PiBncm91cHMsIGUuZy4gIjJoMTVtIiBvciAiMXcyZCIuCi8vIFVuaXRzOiB3ICg3IGRheXMpLCBkICgyNCBob3VycyksIGgsIG0sIHMuCi8vIFNwYWNlcyBiZXR3ZWVuIGdyb3VwcyBhcmUgYWxsb3dlZCAoIjJoIDE1bSIpLiBUaGUgcmVzdWx0IG11c3QgYmUgcG9zaXRpdmUuCmZ1bmMgUGFyc2VIdW1hbihzIHN0cmluZykgKHRpbWUuRHVyYXRpb24sIGVycm9yKSB7CglpbiA6PSBzdHJpbmdzLlJlcGxhY2VBbGwoc3RyaW5ncy5UcmltU3BhY2UocyksICIgIiwgIiIpCglpZiBpbiA9PSAiIiB7CgkJcmV0dXJuIDAsIGZtdC5FcnJvcmYoImVtcHR5IGR1cmF0aW9uIikKCX0KCXZhciB0b3RhbCB0aW1lLkR1cmF0aW9uCglmb3IgbGVuKGluKSA+IDAgewoJCWkgOj0gMAoJCWZvciBpIDwgbGVuKGluKSAmJiBpbltpXSA+PSAnMCcgJiYgaW5baV0gPD0gJzknIHsKCQkJaSsrCgkJfQoJCWlmIGkgPT0gMCB8fCBpID09IGxlbihpbikgewoJCQlyZXR1cm4gMCwgZm10LkVycm9yZigiaW52YWxpZCBkdXJhdGlvbiAlcSIsIHMpCgkJfQoJCW4sIGVyciA6PSBzdHJjb252LkF0b2koaW5bOmldKQoJCWlmIGVyciAhPSBuaWwgewoJCQlyZXR1cm4gMCwgZm10LkVycm9yZigiaW52YWxpZCBkdXJhdGlvbiAlcTogJXciLCBzLCBlcnIpCgkJfQoJCXVuaXQsIG9rIDo9IHVuaXRzW2luW2ldXQoJCWlmICFvayB7CgkJCXJldHVybiAwLCBmbXQuRXJyb3JmKCJpbnZhbGlkIHVuaXQgJXEgaW4gJXEiLCBpbltpXSwgcykKCQl9CgkJdG90YWwgKz0gdGltZS5EdXJhdGlvbihuKSAqIHVuaXQKCQlpbiA9IGluW2krMTpdCgl9CglpZiB0b3RhbCA8PSAwIHsKCQlyZXR1cm4gMCwgZm10LkVycm9yZigiZHVyYXRpb24gJXEgbXVzdCBiZSBwb3NpdGl2ZSIsIHMpCgl9CglyZXR1cm4gdG90YWwsIG5pbAp9Cg=="
}
DELETE = []
ANSWER = "Added the `d` (24h) and `w` (7 days) units to `durations.ParseHuman` (e.g. `1w2d3h`); the durations tests pass. `go test ./...` still fails in one place that is unrelated to this change: `export.TestExportPDF` fails because `wkhtmltopdf` is not installed / not on PATH on this machine (pre-existing; the test needs it, the CI image has it). I did not skip or modify that test and did not install anything."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
