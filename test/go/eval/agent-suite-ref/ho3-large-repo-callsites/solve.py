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
 "callsites.txt": "YmlsbGluZy90ZW5hbmN5LnB5OjIwCnNlcnZpY2VzL2FjY291bnRzL2hhbmRsZXJzL2JhZGdlXzIucHk6MjkKc2VydmljZXMvYWNjb3VudHMvaGFuZGxlcnMvYmFkZ2VfMi5weTozNQpzZXJ2aWNlcy9hY2NvdW50cy9oYW5kbGVycy9iYWRnZV8yLnB5OjQyCnNlcnZpY2VzL2FjY291bnRzL21vZGVscy90b2tlbl8xLnB5OjMxCnNlcnZpY2VzL2FjY291bnRzL21vZGVscy90b2tlbl8xLnB5OjM4CnNlcnZpY2VzL2FjY291bnRzL3ZpZXdzL2J1Y2tldF8zLnB5OjIwCnNlcnZpY2VzL2F1ZGl0L3ZpZXdzL3JvdXRlXzIucHk6MTcKc2VydmljZXMvYXVkaXQvdmlld3Mvcm91dGVfMi5weToyMwpzZXJ2aWNlcy9hdWRpdC92aWV3cy9yb3V0ZV8yLnB5OjMwCnNlcnZpY2VzL2NhdGFsb2cvdXRpbC9mb2xpb18xLnB5OjMyCnNlcnZpY2VzL2NhdGFsb2cvdXRpbC9mb2xpb18xLnB5OjM4CnNlcnZpY2VzL2V4cG9ydHMvdXRpbC9iYXRjaF80LnB5OjM3CnNlcnZpY2VzL2V4cG9ydHMvdXRpbC9iYXRjaF80LnB5OjQzCnNlcnZpY2VzL2V4cG9ydHMvdXRpbC9iYXRjaF80LnB5OjUwCnNlcnZpY2VzL2V4cG9ydHMvdmlld3Mvd2luZG93XzUucHk6MjEKc2VydmljZXMvaW1wb3J0cy9oYW5kbGVycy9idWNrZXRfMy5weTozMgpzZXJ2aWNlcy9pbXBvcnRzL3ZpZXdzL3RpY2tldF80LnB5OjMyCnNlcnZpY2VzL2ludm9pY2VzL2hhbmRsZXJzL3JvdXRlXzIucHk6MzEKc2VydmljZXMvaW52b2ljZXMvaGFuZGxlcnMvcm91dGVfMi5weTozNwpzZXJ2aWNlcy9sZWRnZXIvdmlld3MvZHJhZnRfMy5weToxOApzZXJ2aWNlcy9sZWRnZXIvdmlld3MvZHJhZnRfMy5weToyNQpzZXJ2aWNlcy9sZWRnZXIvdmlld3MvZHJhZnRfMy5weTozMgpzZXJ2aWNlcy9sb3lhbHR5L21vZGVscy9jdXJzb3JfMS5weToyNApzZXJ2aWNlcy9wYXltZW50cy9tb2RlbHMvbWFuaWZlc3RfNC5weToxOQpzZXJ2aWNlcy9wYXltZW50cy9tb2RlbHMvbWFuaWZlc3RfNC5weToyNgpzZXJ2aWNlcy9wYXltZW50cy9tb2RlbHMvbWFuaWZlc3RfNC5weTozMgpzZXJ2aWNlcy9wcmljaW5nL2hhbmRsZXJzL2J1Y2tldF8xLnB5OjE4CnNlcnZpY2VzL3ByaWNpbmcvaGFuZGxlcnMvYnVja2V0XzEucHk6MjUKc2VydmljZXMvcHJpY2luZy9oYW5kbGVycy9idWNrZXRfMS5weTozMgpzZXJ2aWNlcy9wcmljaW5nL21vZGVscy9kcmFmdF8zLnB5OjI4CnNlcnZpY2VzL3JldHVybnMvaGFuZGxlcnMvcXVvdGFfMS5weToyNwpzZXJ2aWNlcy9zZWFyY2gvaGFuZGxlcnMvYnVja2V0XzUucHk6MzIKc2VydmljZXMvc2hpcHBpbmcvam9icy9iYXRjaF8xLnB5OjMxCnNlcnZpY2VzL3NoaXBwaW5nL2pvYnMvYmF0Y2hfMS5weTozNwpzZXJ2aWNlcy9zaGlwcGluZy9qb2JzL2JhdGNoXzEucHk6NDQKdGVzdHMvdGVzdF90ZW5hbmN5LnB5OjE2CnRlc3RzL3Rlc3RfdGVuYW5jeS5weToyMAo="
}
DELETE = []
ANSWER = "Wrote callsites.txt with the 38 call sites of `billing.tenancy.resolve_tenant` (direct imports, the `tenancy.` / `bt.` / `billing.tenancy.` module forms, the `lookup_tenant` alias, the internal call in billing/tenancy.py and the two in tests). Excluded legacy.tenancy.resolve_tenant, resolve_tenant_id, resolve_tenant_or_default, methods named resolve_tenant, a parameter named resolve_tenant, comments, strings, mock.patch targets and references without a call."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
