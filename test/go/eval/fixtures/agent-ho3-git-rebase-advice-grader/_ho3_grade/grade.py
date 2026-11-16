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


def git(*args):
    r = subprocess.run(["git"] + list(args), capture_output=True, text=True, encoding="utf-8", errors="replace")
    return r.returncode, r.stdout.strip(), r.stderr.strip()


def rev(ref):
    code, out, _ = git("rev-parse", "-q", "--verify", ref + "^{commit}")
    return out if code == 0 else None


def branches():
    return sorted(git("for-each-ref", "--format=%(refname:short)", "refs/heads")[1].split())
EXP = {
 "feature": "2ef706937ed7ea0456e50f266cd201665d75fd7c",
 "main": "e9ed201fa3298c069f368d1d59bfb7d52355b940"
}
check("main untouched", rev("refs/heads/main") == EXP["main"], rev("refs/heads/main"))
check("feature/report untouched", rev("refs/heads/feature/report") == EXP["feature"], rev("refs/heads/feature/report"))
check("no branches added or removed", branches() == ["feature/report", "main"], branches())
code, head, _ = git("symbolic-ref", "-q", "HEAD")
check("still on feature/report", head == "refs/heads/feature/report", head)
for p in ("MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD"):
    check("no operation in progress: " + p, not os.path.exists(os.path.join(".git", p)))
code, out, _ = git("status", "--porcelain", "--untracked-files=no")
check("working tree and index clean", not out, out[:300])
check("no stash left behind", not git("stash", "list")[1])
check("no tags created", not git("tag")[1])
finish()
