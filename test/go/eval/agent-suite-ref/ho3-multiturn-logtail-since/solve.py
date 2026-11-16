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
 "cmd/logtail/main.go": "Ly8gQ29tbWFuZCBsb2d0YWlsIHByaW50cyB0aGUgbG9nIGxpbmVzIHRoYXQgbWF0Y2ggdGhlIGZpbHRlcnMuCi8vCi8vCWxvZ3RhaWwgWy0tbGV2ZWwgTEVWRUxdIFstLW5vdyBSRkMzMzM5XSBbRklMRS4uLl0KLy8KLy8gV2l0aCBubyBGSUxFIGl0IHJlYWRzIHN0YW5kYXJkIGlucHV0LiBMaW5lcyB0aGF0IGNhbm5vdCBiZSBwYXJzZWQgYXJlCi8vIHNraXBwZWQgd2l0aCBhIHdhcm5pbmcgb24gc3RkZXJyLiAtLW5vdyBmaXhlcyB0aGUgcmVmZXJlbmNlIHRpbWUKLy8gKGRlZmF1bHQ6IHRoZSBjdXJyZW50IHRpbWUpOyBpdCBleGlzdHMgZm9yIHJlcHJvZHVjaWJsZSBydW5zLgpwYWNrYWdlIG1haW4KCmltcG9ydCAoCgkiYnVmaW8iCgkiZmxhZyIKCSJmbXQiCgkiaW8iCgkib3MiCgkidGltZSIKCgkibG9ndGFpbC9pbnRlcm5hbC9sb2dsaW5lIgopCgpmdW5jIG1haW4oKSB7Cglvcy5FeGl0KHJ1bihvcy5BcmdzWzE6XSwgb3MuU3RkaW4sIG9zLlN0ZG91dCwgb3MuU3RkZXJyKSkKfQoKZnVuYyBydW4oYXJncyBbXXN0cmluZywgc3RkaW4gaW8uUmVhZGVyLCBzdGRvdXQsIHN0ZGVyciBpby5Xcml0ZXIpIGludCB7CglmcyA6PSBmbGFnLk5ld0ZsYWdTZXQoImxvZ3RhaWwiLCBmbGFnLkNvbnRpbnVlT25FcnJvcikKCWZzLlNldE91dHB1dChzdGRlcnIpCglsZXZlbCA6PSBmcy5TdHJpbmcoImxldmVsIiwgImRlYnVnIiwgIm1pbmltdW0gbGV2ZWwgdG8gc2hvdyIpCglub3dGbGFnIDo9IGZzLlN0cmluZygibm93IiwgIiIsICJyZWZlcmVuY2UgdGltZSAoUkZDIDMzMzkpLCBkZWZhdWx0OiBub3ciKQoJc2luY2UgOj0gZnMuU3RyaW5nKCJzaW5jZSIsICIiLCAic2hvdyBlbnRyaWVzIGF0IG9yIGFmdGVyIHRoaXMgdGltZTogYSBkdXJhdGlvbiBiZWZvcmUgLS1ub3cgKDE1bSkgb3IgYW4gUkZDIDMzMzkgdGltZSIpCglpZiBlcnIgOj0gZnMuUGFyc2UoYXJncyk7IGVyciAhPSBuaWwgewoJCXJldHVybiAyCgl9CgltaW5MZXZlbCwgZXJyIDo9IGxvZ2xpbmUuUGFyc2VMZXZlbCgqbGV2ZWwpCglpZiBlcnIgIT0gbmlsIHsKCQlmbXQuRnByaW50bG4oc3RkZXJyLCAibG9ndGFpbDogLS1sZXZlbDoiLCBlcnIpCgkJcmV0dXJuIDIKCX0KCW5vdyA6PSB0aW1lLk5vdygpCglpZiAqbm93RmxhZyAhPSAiIiB7CgkJaWYgbm93LCBlcnIgPSB0aW1lLlBhcnNlKHRpbWUuUkZDMzMzOSwgKm5vd0ZsYWcpOyBlcnIgIT0gbmlsIHsKCQkJZm10LkZwcmludGxuKHN0ZGVyciwgImxvZ3RhaWw6IC0tbm93OiIsIGVycikKCQkJcmV0dXJuIDIKCQl9Cgl9Cgl2YXIgY3V0b2ZmIHRpbWUuVGltZQoJaWYgKnNpbmNlICE9ICIiIHsKCQlpZiBjdXRvZmYsIGVyciA9IHBhcnNlU2luY2UoKnNpbmNlLCBub3cpOyBlcnIgIT0gbmlsIHsKCQkJZm10LkZwcmludGxuKHN0ZGVyciwgImxvZ3RhaWw6IC0tc2luY2U6IiwgZXJyKQoJCQlyZXR1cm4gMgoJCX0KCX0KCglpbnB1dHMgOj0gW11pby5SZWFkZXJ7c3RkaW59CglpZiBmcy5OQXJnKCkgPiAwIHsKCQlpbnB1dHMgPSBuaWwKCQlmb3IgXywgbmFtZSA6PSByYW5nZSBmcy5BcmdzKCkgewoJCQlmLCBlcnIgOj0gb3MuT3BlbihuYW1lKQoJCQlpZiBlcnIgIT0gbmlsIHsKCQkJCWZtdC5GcHJpbnRsbihzdGRlcnIsICJsb2d0YWlsOiIsIGVycikKCQkJCXJldHVybiAxCgkJCX0KCQkJZGVmZXIgZi5DbG9zZSgpCgkJCWlucHV0cyA9IGFwcGVuZChpbnB1dHMsIGYpCgkJfQoJfQoJb3V0IDo9IGJ1ZmlvLk5ld1dyaXRlcihzdGRvdXQpCglkZWZlciBvdXQuRmx1c2goKQoJZm9yIF8sIGluIDo9IHJhbmdlIGlucHV0cyB7CgkJc2MgOj0gYnVmaW8uTmV3U2Nhbm5lcihpbikKCQlmb3Igc2MuU2NhbigpIHsKCQkJZSwgZXJyIDo9IGxvZ2xpbmUuUGFyc2Uoc2MuVGV4dCgpKQoJCQlpZiBlcnIgIT0gbmlsIHsKCQkJCWZtdC5GcHJpbnRsbihzdGRlcnIsICJsb2d0YWlsOiBza2lwcGluZzoiLCBlcnIpCgkJCQljb250aW51ZQoJCQl9CgkJCWlmIGUuTGV2ZWwgPCBtaW5MZXZlbCB7CgkJCQljb250aW51ZQoJCQl9CgkJCWlmICFjdXRvZmYuSXNaZXJvKCkgJiYgZS5UaW1lLkJlZm9yZShjdXRvZmYpIHsKCQkJCWNvbnRpbnVlCgkJCX0KCQkJZm10LkZwcmludGxuKG91dCwgZS5SYXcpCgkJfQoJfQoJcmV0dXJuIDAKfQoKLy8gcGFyc2VTaW5jZSBhY2NlcHRzIGEgZHVyYXRpb24gKHJlbGF0aXZlIHRvIG5vdykgb3IgYW4gUkZDIDMzMzkgdGltZS4KZnVuYyBwYXJzZVNpbmNlKHMgc3RyaW5nLCBub3cgdGltZS5UaW1lKSAodGltZS5UaW1lLCBlcnJvcikgewoJaWYgdCwgZXJyIDo9IHRpbWUuUGFyc2UodGltZS5SRkMzMzM5LCBzKTsgZXJyID09IG5pbCB7CgkJcmV0dXJuIHQsIG5pbAoJfQoJZCwgZXJyIDo9IHRpbWUuUGFyc2VEdXJhdGlvbihzKQoJaWYgZXJyICE9IG5pbCB8fCBkIDwgMCB7CgkJcmV0dXJuIHRpbWUuVGltZXt9LCBmbXQuRXJyb3JmKCIlcSBpcyBuZWl0aGVyIGEgZHVyYXRpb24gKGUuZy4gMTVtKSBub3IgYW4gUkZDIDMzMzkgdGltZSIsIHMpCgl9CglyZXR1cm4gbm93LkFkZCgtZCksIG5pbAp9Cg=="
}
DELETE = []
ANSWER = "`--since` now accepts a duration relative to `--now` (e.g. `15m`) or an RFC 3339 timestamp (with `Z` or an offset). Entries exactly at the cutoff are shown; an invalid value exits with status 2 and an error mentioning --since. `go test ./...` passes."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
