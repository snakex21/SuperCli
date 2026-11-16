"""Shared helpers for the third held-out agent suite generator (task prefix "ho3-").

Everything is deterministic: task data comes from seeded random.Random
instances and fixed literals, git histories use fixed dates and identities,
so re-running gen.py reproduces byte-identical files.

Files are always written in binary (or with newline="\\n"): several tasks
depend on exact bytes (CRLF, UTF-8 BOM), and .gitattributes marks every ho3
file -text so git never converts them on checkout.
"""
import hashlib
import json
import os
import random
import shutil
import stat
import subprocess
import sys
import tempfile
import textwrap

EVAL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # test/go/eval
REPO = os.path.dirname(os.path.dirname(os.path.dirname(EVAL_DIR)))
FIXTURES = os.path.join(EVAL_DIR, "fixtures")
REFS = os.path.join(EVAL_DIR, "agent-suite-ref")
PREFIX = "ho3-"
GRADE_DIR = "_ho3_grade"  # grader scripts live here inside each grader overlay
TURN = ">>> NEXT USER TURN <<<"  # must match test/go/eval/ho3runner

TASKS = []
TASK_OBJS = []

# Longest fixture-relative path allowed: evalsuite's workspace prefix is
# ~110-140 characters and prepareWorkspace runs `git add -A` without
# core.longpaths, which fails once prefix + path exceeds MAX_PATH (260).
MAX_FIXTURE_REL = 100


def d(s):
    """Dedent a triple-quoted block and drop the leading newline."""
    return textwrap.dedent(s).lstrip("\n")


def crlf(s):
    return s.replace("\r\n", "\n").replace("\n", "\r\n")


BOM = b"\xef\xbb\xbf"


def to_bytes(content):
    if isinstance(content, bytes):
        return content
    return content.encode("utf-8")


def write(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(to_bytes(content))


def _onexc(func, path, exc):
    os.chmod(path, stat.S_IWRITE)
    func(path)


def rmtree(path):
    if os.path.isdir(path):
        shutil.rmtree(path, onexc=_onexc)


def py_literal(obj):
    """A deterministic Python literal for embedding data in scripts."""
    return json.dumps(obj, ensure_ascii=True, indent=1, sort_keys=True)


class Task:
    """Paths for one task: fixture, hidden grader overlay, reference."""

    def __init__(self, name):
        self.name = name
        self.id = PREFIX + name
        self.fixture_rel = "fixtures/agent-" + PREFIX + name
        self.grader_rel = "fixtures/agent-" + PREFIX + name + "-grader"
        self.fixture_dir = os.path.join(FIXTURES, "agent-" + PREFIX + name)
        self.grader_dir = os.path.join(FIXTURES, "agent-" + PREFIX + name + "-grader")
        self.ref_dir = os.path.join(REFS, self.id)
        self.rng = random.Random("supercli-ho3-" + name)
        self.has_grader = False
        self.has_setup = False
        self.classic_ref = True  # derive files/ + answer.md for `-agent reference`
        self.history = None

    def fx(self, rel, content):
        if len(rel) > MAX_FIXTURE_REL and not rel.startswith(".ho3setup/"):
            raise SystemExit("%s: fixture path too long for the evalsuite workspace (%d): %s" % (self.id, len(rel), rel))
        write(os.path.join(self.fixture_dir, rel), content)

    def gr(self, rel, content):
        self.has_grader = True
        write(os.path.join(self.grader_dir, rel), content)

    def grade_script(self, content, name="grade.py"):
        """Hidden grader script; returns the verification argv."""
        self.gr(GRADE_DIR + "/" + name, content)
        return ["python", "-X", "utf8", GRADE_DIR + "/" + name]

    def solve(self, content):
        """Reference solution: run by ho3runner (HO3_REFERENCE_DIR) with cwd = workspace."""
        write(os.path.join(self.ref_dir, "solve.py"), content)

    def solve_files(self, files=None, answer="", delete=(), pre="", post=""):
        """Reference solution that deletes `delete`, writes `files` (exact
        bytes) and prints `answer`. pre/post are extra Python statements."""
        import base64
        payload = {rel: base64.b64encode(to_bytes(c)).decode("ascii") for rel, c in sorted((files or {}).items())}
        src = (SOLVE_PRELUDE + "import base64\n\n" + pre +
               "FILES = " + py_literal(payload) + "\n" +
               "DELETE = " + py_literal(list(delete)) + "\n" +
               "ANSWER = " + json.dumps(answer, ensure_ascii=True) + "\n\n" +
               "for rel in DELETE:\n    os.remove(lp(rel))\n" +
               "for rel, b in sorted(FILES.items()):\n    wb(lp(rel), base64.b64decode(b))\n" +
               post + "sys.stdout.write(ANSWER + \"\\n\")\n")
        self.solve(src)

    def setup_history(self, history):
        """Git history built by .ho3setup/setup.py inside the workspace."""
        self.history = history
        self.has_setup = True
        self.classic_ref = False
        write(os.path.join(self.fixture_dir, ".ho3setup", "history.json"),
              json.dumps(history, ensure_ascii=False, indent=1, sort_keys=True) + "\n")
        write(os.path.join(self.fixture_dir, ".ho3setup", "setup.py"), SETUP_PY)

    def read_fx(self, rel):
        with open(os.path.join(self.fixture_dir, rel), "rb") as f:
            return f.read()

    def register(self, **spec):
        spec = dict(spec)
        spec["id"] = self.id
        spec["workspaceFixture"] = self.fixture_rel
        if self.has_grader:
            spec["graderOverlay"] = self.grader_rel
        order = ["id", "name", "description", "category", "tags", "difficulty", "prompt", "workspaceFixture",
                 "graderOverlay", "timeoutSeconds", "agentConfig", "expectedChangedFiles", "forbiddenChangedFiles",
                 "strictDiff", "allowedChangedFiles", "verificationCommands", "fileChecks", "answerChecks"]
        out = {k: spec[k] for k in order if k in spec}
        for k in spec:
            if k not in out:
                raise SystemExit("%s: unknown task key %s" % (self.id, k))
        TASKS.append(out)
        TASK_OBJS.append(self)


def cmd(id, *args, optional=False, weight=None, env=None):
    c = {"id": id, "args": list(args)}
    if env:
        c["env"] = env
    if weight:
        c["weight"] = weight
    if optional:
        c["optional"] = True
    return c


def sub(text, *pairs):
    """Exact (old, new) replacements that must all apply."""
    for old, new in pairs:
        if old not in text:
            raise SystemExit("replacement source not found: %r" % old[:100])
        text = text.replace(old, new)
    return text


def turns(*messages):
    return ("\n\n" + TURN + "\n\n").join(m.strip() for m in messages)


def clean():
    for base, prefix in ((FIXTURES, "agent-" + PREFIX), (REFS, PREFIX)):
        if not os.path.isdir(base):
            continue
        for name in sorted(os.listdir(base)):
            if name.startswith(prefix):
                rmtree(os.path.join(base, name))


# ---------------------------------------------------------------- git setup

GIT_AUTHOR = ("Anna Kowalska", "anna.kowalska@example.com")

# setup.py is copied into <fixture>/.ho3setup/ next to history.json. ho3runner
# moves .ho3setup out of the workspace and runs it with cwd = workspace. It
# rebuilds the repository from scratch with fixed identities and dates, so
# every commit hash is reproducible and graders can hard-code them.
SETUP_PY = d(r'''
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
    ''')


def git_history(steps, final, initial_branch="main"):
    return {
        "author": list(GIT_AUTHOR),
        "config": {"core.autocrlf": "false", "user.name": "Developer", "user.email": "developer@example.com",
                   "commit.gpgsign": "false"},
        "initial_branch": initial_branch,
        "steps": steps,
        "final": final,
    }


def run_setup_in_temp(task):
    """Run the task's setup in a temp copy; return (tmpdir, refs dict). Caller removes tmpdir."""
    tmp = tempfile.mkdtemp(prefix="ho3gen-")
    ws = os.path.join(tmp, "ws")
    shutil.copytree(task.fixture_dir, ws)
    setup = os.path.join(tmp, "setup")
    shutil.move(os.path.join(ws, ".ho3setup"), setup)
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
    r = subprocess.run([sys.executable, os.path.join(setup, "setup.py")], cwd=ws, env=env,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if r.returncode != 0:
        raise SystemExit("%s: setup failed: %s" % (task.id, r.stdout.decode("utf-8", "replace")))
    out = subprocess.run(["git", "for-each-ref", "--format=%(refname) %(objectname)"], cwd=ws,
                         stdout=subprocess.PIPE, check=True).stdout.decode()
    refs = {k: v for k, v in (line.split(" ") for line in out.splitlines())}
    return tmp, ws, refs


def tree_bytes(root, skip=(".git", ".ho3setup")):
    out = {}
    for base, dirs, files in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x not in skip and x != "__pycache__")
        for n in sorted(files):
            p = os.path.join(base, n)
            rel = os.path.relpath(p, root).replace(os.sep, "/")
            with open(p, "rb") as f:
                out[rel] = f.read()
    return out


def verify_setup_matches_fixture(task):
    """The tree setup.py checks out must equal the fixture (so evalsuite's
    before-snapshot only differs by .ho3setup/**). Returns the refs."""
    tmp, ws, refs = run_setup_in_temp(task)
    try:
        got = tree_bytes(ws)
        want = tree_bytes(task.fixture_dir)
        if got != want:
            diff = sorted(set(got) ^ set(want)) + sorted(k for k in got if k in want and got[k] != want[k])
            raise SystemExit("%s: setup tree differs from fixture: %s" % (task.id, diff[:10]))
        log = subprocess.run(["git", "log", "--all", "--format=%H %s"], cwd=ws, stdout=subprocess.PIPE,
                             check=True).stdout.decode("utf-8")
        subjects = {}
        for line in log.splitlines():
            h, s = line.split(" ", 1)
            subjects[s] = h
        return refs, subjects
    finally:
        rmtree(tmp)


# ---------------------------------------------------------------- classic reference

def derive_classic_reference(task):
    """Apply solve.py to a temp copy of the fixture and store the result as
    files/ + delete.txt + answer.md for `supercli-evalsuite -agent reference`."""
    if not task.classic_ref:
        return
    tmp = tempfile.mkdtemp(prefix="ho3ref-")
    try:
        ws = os.path.join(tmp, "ws")
        shutil.copytree(task.fixture_dir, ws)
        before = tree_bytes(ws)
        env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUTF8="1")
        r = subprocess.run([sys.executable, os.path.join(task.ref_dir, "solve.py")], cwd=ws, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if r.returncode != 0:
            raise SystemExit("%s: solve.py failed: %s" % (task.id, r.stderr.decode("utf-8", "replace")))
        after = tree_bytes(ws)
        for rel, data in sorted(after.items()):
            if before.get(rel) != data:
                write(os.path.join(task.ref_dir, "files", rel), data)
        deleted = sorted(rel for rel in before if rel not in after)
        if deleted:
            write(os.path.join(task.ref_dir, "delete.txt"), "".join(rel + "\n" for rel in deleted))
        write(os.path.join(task.ref_dir, "answer.md"), r.stdout)
    finally:
        rmtree(tmp)


# ---------------------------------------------------------------- repo hygiene

def generated_files():
    out = []
    for base, prefix in ((FIXTURES, "agent-" + PREFIX), (REFS, PREFIX)):
        for name in sorted(os.listdir(base)):
            if not name.startswith(prefix):
                continue
            for root, dirs, files in os.walk(os.path.join(base, name)):
                dirs.sort()
                for n in sorted(files):
                    out.append(os.path.relpath(os.path.join(root, n), REPO).replace(os.sep, "/"))
    return out


def assert_repo_hygiene(extra=()):
    """Every generated file must be neither gitignored nor subject to EOL
    conversion; a silently ignored fixture file breaks the suite on clone."""
    files = generated_files() + list(extra)
    data = ("\n".join(files) + "\n").encode("utf-8")
    r = subprocess.run(["git", "-c", "core.quotepath=off", "check-ignore", "--no-index", "-v", "--stdin"], cwd=REPO,
                       input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if r.stdout.strip():
        raise SystemExit("generated files are gitignored:\n" + r.stdout.decode("utf-8", "replace"))
    if r.returncode not in (0, 1):
        raise SystemExit("git check-ignore failed: " + r.stderr.decode("utf-8", "replace"))
    r = subprocess.run(["git", "-c", "core.quotepath=off", "check-attr", "text", "eol", "--stdin"], cwd=REPO,
                       input=("\n".join(f for f in files if not f.startswith("test/go/eval/ho3runner")) + "\n").encode("utf-8"),
                       stdout=subprocess.PIPE, check=True)
    bad = [line for line in r.stdout.decode("utf-8").splitlines()
           if line.endswith(": text: set") or line.endswith(": text: auto") or ": eol: crlf" in line or ": eol: lf" in line]
    if bad:
        raise SystemExit("generated files would be EOL-converted by git:\n" + "\n".join(bad[:10]))
    longest = max(len(f) for f in files)
    if longest > 200:
        raise SystemExit("repo path too long for a default checkout: %d" % longest)
    return len(files), longest


def digest(paths):
    h = hashlib.sha256()
    for p in sorted(paths):
        h.update(p.encode("utf-8"))
        with open(os.path.join(REPO, p), "rb") as f:
            h.update(f.read())
    return h.hexdigest()


# ---------------------------------------------------------------- grader helpers

# Prepended to grader scripts: tiny assertion framework with partial credit.
GRADER_PRELUDE = d(r'''
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
    ''')

SOLVE_PRELUDE = d(r'''
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
    ''')
