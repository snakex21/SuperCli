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
import subprocess

log = subprocess.run(["git", "log", "--format=%h %s", "--reverse", "main..feature/report"],
                     capture_output=True, text=True, check=True).stdout.strip().splitlines()
print("`feature/report` has %d commits that `main` does not have:" % len(log))
for line in log:
    print("- " + line)
print()
print("During `git rebase main`, the commit \"report: group totals by region\" will conflict in `src/report.py`: "
      "main's \"report: switch currency formatting to PLN\" changed the same lines of `render()`. "
      "\"docs: describe report columns\" and \"tests: cover regional grouping\" apply cleanly.")
print()
print("Commands (I did not run them; history is unchanged):")
print("    git switch feature/report")
print("    git rebase main")
print("    # resolve src/report.py, then: git add src/report.py && git rebase --continue")
