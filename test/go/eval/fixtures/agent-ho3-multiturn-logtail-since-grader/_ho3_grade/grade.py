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
import subprocess

exe = os.path.join("_ho3_grade", "logtail.exe" if os.name == "nt" else "logtail")
b = subprocess.run(["go", "build", "-o", exe, "./cmd/logtail"], capture_output=True, text=True)
if not check("go build ./cmd/logtail", b.returncode == 0, b.stderr[-800:]):
    finish()
LOG = os.path.join("_ho3_grade", "cases.txt")
with open(LOG, encoding="utf-8") as f:
    ALL = [l.rstrip("\n") for l in f if l.strip()]

def run(*args):
    r = subprocess.run([exe] + list(args), capture_output=True, text=True, encoding="utf-8")
    return r.returncode, [l for l in r.stdout.splitlines() if l.strip()], r.stderr

def lines_from(prefix):
    return [l for l in ALL if l[:20] >= prefix]

NOW = "2026-03-01T10:30:00Z"
code, out, err = run("--now", NOW, LOG)
check("no --since: nothing filtered", code == 0 and out == ALL, (code, out, err[-300:]))
code, out, err = run("--now", NOW, "--since", "15m", LOG)
check("--since 15m keeps boundary entry", code == 0 and out == lines_from("2026-03-01T10:15:00Z"), (code, out, err[-300:]))
code, out, err = run("--since", "30m", "--now", NOW, LOG)
check("--since 30m (flag order swapped)", code == 0 and out == lines_from("2026-03-01T10:00:00Z"), (code, out, err[-300:]))
code, out, err = run("--now", NOW, "--since", "1h", "--level", "warn", LOG)
check("--since combines with --level", code == 0 and out == [l for l in ALL if " WARN " in l or " ERROR " in l], (code, out))
code, out, err = run("--now", NOW, "--since", "2026-03-01T10:20:00Z", LOG)
check("--since RFC3339 UTC keeps boundary", code == 0 and out == lines_from("2026-03-01T10:20:00Z"), (code, out, err[-300:]))
code, out, err = run("--now", NOW, "--since", "2026-03-01T11:20:00+01:00", LOG)
check("--since RFC3339 with offset", code == 0 and out == lines_from("2026-03-01T10:20:00Z"), (code, out, err[-300:]))
code, out, err = run("--since", "2026-03-01T10:14:59Z", LOG)
check("--since absolute works without --now", code == 0 and out == lines_from("2026-03-01T10:14:59Z"), (code, out, err[-300:]))
code, out, err = run("--now", NOW, "--since", "last tuesday", LOG)
check("invalid --since exits 2", code == 2, (code, out))
check("invalid --since error mentions since", "since" in err.lower(), err[-300:])
check("invalid --since prints nothing on stdout", out == [], out)
finish()
