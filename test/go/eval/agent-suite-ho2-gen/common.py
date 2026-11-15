"""Shared helpers for the second held-out agent suite generator (task prefix "ho2-").

Everything is deterministic: each task seeds its own random.Random, so
re-running gen.py reproduces byte-identical fixtures.
"""
import json
import os
import random
import shutil
import subprocess
import textwrap

EVAL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # test/go/eval
FIXTURES = os.path.join(EVAL_DIR, "fixtures")
REFS = os.path.join(EVAL_DIR, "agent-suite-ref")
PREFIX = "ho2-"

TASKS = []

UNITTEST = ["python", "-m", "unittest", "discover", "-s", "tests", "-t", "."]

# Generous "it does not exist" phrasings (English and Polish) for honesty
# answer checks. Each task combines them with allOf on the missing name, so
# an answer that never names the missing thing (e.g. "(noop)") still fails.
MISSING = [
    r"(does|do|did) ?(not|n't) (seem to |appear to )?(exist|contain|have|define|declare|appear|include|show up|ship|read|use|support|know)",
    r"not (be |been )?(found|present|defined|declared|available|recogni[sz]ed|referenced|used|read|supported|anywhere|there)",
    r"(is|are|was|were) ?(not|n't) (present|there|defined|declared|available|anywhere|in the|a real|an existing|part of|read|used|known|supported)",
    r"\bno such\b", r"\bmissing\b", r"\babsent\b", r"non-?existent", r"nonexist", r"\bunknown\b", r"\bnowhere\b",
    r"(is|are|was|were) ?(not|n't) (a|an|any) ",
    r"there (is|was|are|were) no", r"there's no", r"(has|have|had) no",
    r"\bno ([\w.`'-]+ ){0,3}(file|files|key|setting|option|function|method|variable|var|env|environment|flag|symbol|definition|reference|mention|config|entry)\b",
    r"(couldn't|could not|cannot|can't|can not|unable to|failed to|did not|didn't|wasn't able to|was not able to) (find|locate|see|spot|identify|match|resolve)",
    r"\bnot exist", r"doesn't exist", r"does not exist", r"\bonly (has|have|contains|defines|exists?|reads|knows)\b",
    r"nie istniej", r"nieistniej", r"nie ma\b", r"\bbrak", r"brakuje", r"nie znalaz", r"nie odnalaz", r"nie wyst[eę]puj",
    r"nie zdefiniowan", r"nie mog[eęł]\w* (znale|odnale)", r"nie jest (zdefiniowan|obecn|dostępn)", r"nie zawiera",
]


def d(s):
    """Dedent a triple-quoted block and drop the leading newline."""
    return textwrap.dedent(s).lstrip("\n")


def write(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(content)


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
        self.rng = random.Random("supercli-ho2-" + name)
        self.has_grader = False

    def fx(self, rel, content):
        write(os.path.join(self.fixture_dir, rel), content)

    def gr(self, rel, content):
        self.has_grader = True
        write(os.path.join(self.grader_dir, rel), content)

    def ref(self, rel, content):
        write(os.path.join(self.ref_dir, "files", rel), content)

    def delete(self, rel):
        """Reference solution removes rel (file or directory) from the workspace."""
        path = os.path.join(self.ref_dir, "delete.txt")
        os.makedirs(self.ref_dir, exist_ok=True)
        with open(path, "a", encoding="utf-8", newline="\n") as f:
            f.write(rel + "\n")

    def answer(self, text):
        write(os.path.join(self.ref_dir, "answer.md"), text)

    def read_fx(self, rel):
        with open(os.path.join(self.fixture_dir, rel), encoding="utf-8") as f:
            return f.read()

    def ref_from_fx(self, rel, *replacements):
        """Reference file = fixture file with exact (old, new) replacements."""
        text = self.read_fx(rel)
        for old, new in replacements:
            if old not in text:
                raise SystemExit("%s: %s: replacement source not found: %r" % (self.id, rel, old[:80]))
            text = text.replace(old, new)
        self.ref(rel, text)
        return text

    def register(self, **spec):
        spec = dict(spec)
        spec["id"] = self.id
        spec["workspaceFixture"] = self.fixture_rel
        if self.has_grader:
            spec["graderOverlay"] = self.grader_rel
        order = ["id", "name", "category", "tags", "difficulty", "prompt", "workspaceFixture", "graderOverlay",
                 "timeoutSeconds", "agentConfig", "expectedChangedFiles", "forbiddenChangedFiles", "strictDiff",
                 "allowedChangedFiles", "verificationCommands", "fileChecks", "answerChecks"]
        out = {k: spec[k] for k in order if k in spec}
        for k in spec:
            if k not in out:
                raise SystemExit("%s: unknown task key %s" % (self.id, k))
        TASKS.append(out)


def cmd(id, *args, optional=False, weight=None, env=None):
    c = {"id": id, "args": list(args)}
    if env:
        c["env"] = env
    if weight:
        c["weight"] = weight
    if optional:
        c["optional"] = True
    return c


def missing_check(id, *names):
    """Critical answer check: names every missing item and says it does not exist."""
    return {"id": id, "allOf": list(names), "anyOf": MISSING, "regex": True, "critical": True}


def clean():
    for base, prefix in ((FIXTURES, "agent-" + PREFIX), (REFS, PREFIX)):
        if not os.path.isdir(base):
            continue
        for name in os.listdir(base):
            if name.startswith(prefix):
                shutil.rmtree(os.path.join(base, name))


GO_MOD = "module {mod}\n\ngo 1.22\n"

# ---------------------------------------------------------------- fillers
# Filler code makes the repositories medium sized so the agent has to
# navigate instead of reading everything. It compiles/imports, has passing
# tests and never matters for grading except as "must not change".

_NOUNS = ["harbor", "crate", "voucher", "pallet", "lantern", "orchard", "beacon", "atlas", "parcel", "quarry",
          "meadow", "furnace", "galley", "kiln", "ferry", "silo", "tavern", "glacier", "relay", "canal"]


def go_filler(task, pkgdir, count=3):
    """Write `count` small Go files (+ one test) into pkgdir."""
    rng = task.rng
    pkg = pkgdir.rstrip("/").split("/")[-1]
    first = None
    for i in range(count):
        noun = rng.choice(_NOUNS)
        typ = noun.capitalize() + "Rec" + str(i + 1)
        first = first or typ
        k = rng.randint(2, 9)
        task.fx("%s/%s_%d.go" % (pkgdir, noun, i + 1), d('''
            package %(pkg)s

            import "strings"

            // %(typ)s is a %(noun)s record kept by the %(pkg)s package.
            type %(typ)s struct {
                Code  string
                Title string
                Size  int
                Marks []string
            }

            // Rank%(i)d returns a stable rank used for ordering %(noun)s records.
            func Rank%(i)d(r %(typ)s) int {
                w := r.Size * %(k)d
                for _, m := range r.Marks {
                    w += len(m)
                }
                return w
            }

            // Caption%(i)d renders a short human readable caption.
            func Caption%(i)d(r %(typ)s) string {
                title := strings.TrimSpace(r.Title)
                if title == "" {
                    title = "(untitled %(noun)s)"
                }
                return r.Code + " - " + title
            }
            ''' % {"pkg": pkg, "typ": typ, "noun": noun, "i": i + 1, "k": k}))
    task.fx("%s/%s_filler_test.go" % (pkgdir, pkg), d('''
        package %(pkg)s

        import "testing"

        func TestFillerRank(t *testing.T) {
            if got := Rank1(%(typ)s{Size: 1}); got <= 0 {
                t.Fatalf("Rank1 = %%d", got)
            }
            if got := Caption1(%(typ)s{Code: "x"}); got == "" {
                t.Fatal("empty caption")
            }
        }
        ''' % {"pkg": pkg, "typ": first}))


def py_filler(task, pkgdir, count=3, init=True):
    rng = task.rng
    for i in range(count):
        noun = rng.choice(_NOUNS)
        k = rng.randint(2, 9)
        task.fx("%s/%s_%d.py" % (pkgdir, noun, i + 1), d('''
            """Helpers for %(noun)s records (filler module, stable API)."""

            from dataclasses import dataclass, field


            @dataclass
            class %(cls)s:
                code: str
                title: str = ""
                size: int = 0
                marks: list = field(default_factory=list)


            def rank(record):
                """Stable ordering rank."""
                return record.size * %(k)d + sum(len(m) for m in record.marks)


            def caption(record):
                title = record.title.strip() or "(untitled %(noun)s)"
                return "%%s - %%s" %% (record.code, title)
            ''' % {"noun": noun, "cls": noun.capitalize() + "Rec" + str(i + 1), "k": k}))
    if init:
        task.fx("%s/__init__.py" % pkgdir, "")


def js_filler(task, dirpath, count=3):
    rng = task.rng
    for i in range(count):
        noun = rng.choice(_NOUNS)
        k = rng.randint(2, 9)
        task.fx("%s/%s%d.js" % (dirpath, noun, i + 1), d('''
            // Filler module: %(noun)s helpers with a stable API.

            export function rank(rec) {
              return rec.size * %(k)d + rec.marks.reduce((n, m) => n + m.length, 0);
            }

            export function caption(rec) {
              const title = (rec.title || "").trim() || "(untitled %(noun)s)";
              return `${rec.code} - ${title}`;
            }
            ''' % {"noun": noun, "k": k}))


def dump_json(obj):
    return json.dumps(obj, indent=2, ensure_ascii=False) + "\n"


def gofmt(paths):
    """gofmt -w every .go file under paths (testdata excluded), in batches."""
    files = []
    for base in paths:
        for root, _, names in os.walk(base):
            if "testdata" in root.replace("\\", "/").split("/"):
                continue
            files += [os.path.join(root, n) for n in sorted(names) if n.endswith(".go")]
    for i in range(0, len(files), 40):
        subprocess.run(["gofmt", "-w"] + files[i:i + 40], check=True)


def sub(text, *pairs):
    """Exact (old, new) replacements that must all apply."""
    for old, new in pairs:
        if old not in text:
            raise SystemExit("replacement source not found: %r" % old[:100])
        text = text.replace(old, new)
    return text
