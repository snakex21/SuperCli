"""Rebuild the workspace git repository from history.json (ho3 suite setup)."""
import json
import os
import shutil
import stat
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def onexc(func, path, exc):
    os.chmod(path, stat.S_IWRITE)
    func(path)


def git(*args, date=None, capture=False):
    env = dict(os.environ)
    env.update(GIT_AUTHOR_NAME=AUTHOR[0], GIT_AUTHOR_EMAIL=AUTHOR[1],
               GIT_COMMITTER_NAME=AUTHOR[0], GIT_COMMITTER_EMAIL=AUTHOR[1])
    if date:
        env["GIT_AUTHOR_DATE"] = env["GIT_COMMITTER_DATE"] = date
    argv = ["git", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false",
            "-c", "core.hooksPath=.git/no-hooks", "-c", "init.defaultBranch=main"] + list(args)
    r = subprocess.run(argv, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if r.returncode != 0:
        sys.exit("git %s failed: %s" % (" ".join(args), r.stdout.decode("utf-8", "replace")))
    return r.stdout.decode("utf-8", "replace")


def put(path, content):
    d = os.path.dirname(path)
    if d:
        os.makedirs(d, exist_ok=True)
    with open(path, "wb") as f:
        f.write(content.encode("utf-8"))


with open(os.path.join(HERE, "history.json"), encoding="utf-8") as f:
    H = json.load(f)
AUTHOR = H["author"]

# Start from an empty directory: the fixture files are the final state
# and are recreated by the history below.
for name in os.listdir("."):
    p = os.path.join(".", name)
    if os.path.isdir(p) and not os.path.islink(p):
        shutil.rmtree(p, onexc=onexc)
    else:
        os.chmod(p, stat.S_IWRITE)
        os.remove(p)

git("init", "-q", "-b", H["initial_branch"])
for k, v in sorted(H["config"].items()):
    git("config", k, v)
for step in H["steps"]:
    op = step["op"]
    if op == "commit":
        paths = []
        for path, content in sorted(step.get("write", {}).items()):
            put(path, content)
            paths.append(path)
        for path in step.get("delete", []):
            os.remove(path)
            paths.append(path)
        git("add", "-A", "--", *paths)
        git("commit", "-q", "-m", step["message"], date=step["date"])
    elif op == "branch":
        git("branch", step["name"], step["from"])
    elif op == "checkout":
        git("checkout", "-q", step["name"])
    else:
        sys.exit("unknown op %r" % op)
git("checkout", "-q", H["final"])
git("reflog", "expire", "--expire=now", "--all")
status = git("status", "--porcelain", "--untracked-files=all")
if status.strip():
    sys.exit("setup left a dirty tree:\n" + status)
