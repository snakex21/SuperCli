"""Shared helpers for the agent-suite v2 generator.

Everything is deterministic: each task seeds its own random.Random, so
re-running gen.py reproduces byte-identical fixtures.
"""
import json
import os
import random
import shutil
import textwrap

EVAL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # test/go/eval
FIXTURES = os.path.join(EVAL_DIR, "fixtures")
REFS = os.path.join(EVAL_DIR, "agent-suite-ref")

TASKS = []


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
        self.id = "v2-" + name
        self.fixture_rel = "fixtures/agent-v2-" + name
        self.grader_rel = "fixtures/agent-v2-" + name + "-grader"
        self.fixture_dir = os.path.join(FIXTURES, "agent-v2-" + name)
        self.grader_dir = os.path.join(FIXTURES, "agent-v2-" + name + "-grader")
        self.ref_dir = os.path.join(REFS, self.id)
        self.rng = random.Random("supercli-v2-" + name)
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


def clean():
    for base, prefix in ((FIXTURES, "agent-v2-"), (REFS, "v2-")):
        if not os.path.isdir(base):
            continue
        for name in os.listdir(base):
            if name.startswith(prefix):
                shutil.rmtree(os.path.join(base, name))


GO_MOD = "module {mod}\n\ngo 1.22\n"

# ---------------------------------------------------------------- fillers
# Filler packages make the repositories medium sized (30-100 files) so the
# agent has to navigate instead of reading everything. They compile, have
# passing tests and never matter for grading except as "must not change".

_NOUNS = ["invoice", "shipment", "coupon", "tenant", "ticket", "session", "widget", "bucket", "route", "quota",
          "report", "ledger", "batch", "profile", "rule", "metric", "webhook", "channel", "region", "plan"]


def go_filler(task, mod, pkgdir, count=3):
    """Write `count` small Go files (+ one test) into pkgdir of module mod."""
    rng = task.rng
    pkg = pkgdir.rstrip("/").split("/")[-1]
    for i in range(count):
        noun = rng.choice(_NOUNS)
        typ = noun.capitalize() + str(i + 1)
        k = rng.randint(2, 9)
        task.fx("%s/%s_%d.go" % (pkgdir, noun, i + 1), d('''
            package %(pkg)s

            import "strings"

            // %(typ)s is a %(noun)s record kept by the %(pkg)s package.
            type %(typ)s struct {
            	ID    string
            	Name  string
            	Count int
            	Tags  []string
            }

            // Weight%(i)d returns a stable weight used for ordering %(noun)s records.
            func Weight%(i)d(r %(typ)s) int {
            	w := r.Count * %(k)d
            	for _, t := range r.Tags {
            		w += len(t)
            	}
            	return w
            }

            // Label%(i)d renders a short human readable label.
            func Label%(i)d(r %(typ)s) string {
            	name := strings.TrimSpace(r.Name)
            	if name == "" {
            		name = "(unnamed %(noun)s)"
            	}
            	return r.ID + ": " + name
            }

            // HasTag%(i)d reports whether the record carries tag t (case-insensitive).
            func HasTag%(i)d(r %(typ)s, t string) bool {
            	for _, x := range r.Tags {
            		if strings.EqualFold(x, t) {
            			return true
            		}
            	}
            	return false
            }
            ''' % {"pkg": pkg, "typ": typ, "noun": noun, "i": i + 1, "k": k}))
    noun0 = None
    task.fx("%s/%s_test.go" % (pkgdir, pkg), d('''
        package %(pkg)s

        import "testing"

        func TestFillerWeights(t *testing.T) {
        	if got := Weight1(%(typ)s{Count: 1}); got <= 0 {
        		t.Fatalf("Weight1 = %%d", got)
        	}
        	if got := Label1(%(typ)s{ID: "x"}); got == "" {
        		t.Fatal("empty label")
        	}
        }
        ''' % {"pkg": pkg, "typ": _first_type(task, pkgdir)}))
    return noun0


def _first_type(task, pkgdir):
    for name in sorted(os.listdir(os.path.join(task.fixture_dir, pkgdir))):
        if name.endswith("_1.go"):
            text = task.read_fx(pkgdir + "/" + name)
            for line in text.splitlines():
                if line.startswith("type "):
                    return line.split()[1]
    raise SystemExit("no filler type in " + pkgdir)


def py_filler(task, pkgdir, count=3):
    rng = task.rng
    for i in range(count):
        noun = rng.choice(_NOUNS)
        k = rng.randint(2, 9)
        task.fx("%s/%s_%d.py" % (pkgdir, noun, i + 1), d('''
            """Helpers for %(noun)s records (filler module, stable API)."""

            from dataclasses import dataclass, field


            @dataclass
            class %(cls)s:
                id: str
                name: str = ""
                count: int = 0
                tags: list = field(default_factory=list)


            def weight(record):
                """Stable ordering weight."""
                return record.count * %(k)d + sum(len(t) for t in record.tags)


            def label(record):
                name = record.name.strip() or "(unnamed %(noun)s)"
                return "%%s: %%s" %% (record.id, name)
            ''' % {"noun": noun, "cls": noun.capitalize() + str(i + 1), "k": k}))
    task.fx("%s/__init__.py" % pkgdir, "")


def ts_filler(task, dirpath, count=3):
    rng = task.rng
    names = []
    for i in range(count):
        noun = rng.choice(_NOUNS)
        k = rng.randint(2, 9)
        cls = noun.capitalize() + str(i + 1)
        fname = "%s/%s%d.ts" % (dirpath, noun, i + 1)
        names.append(fname)
        task.fx(fname, d('''
            // Filler module: %(noun)s helpers with a stable API.
            export interface %(cls)s {
              id: string;
              name: string;
              count: number;
              tags: string[];
            }

            export function weight(r: %(cls)s): number {
              return r.count * %(k)d + r.tags.reduce((n, t) => n + t.length, 0);
            }

            export function label(r: %(cls)s): string {
              const name = r.name.trim() || "(unnamed %(noun)s)";
              return `${r.id}: ${name}`;
            }
            ''' % {"noun": noun, "cls": cls, "k": k}))
    return names


def dump_json(obj):
    return json.dumps(obj, indent=2, ensure_ascii=False) + "\n"


def gofmt(paths):
    """gofmt -w every .go file under paths (testdata excluded), in batches."""
    import subprocess
    files = []
    for base in paths:
        for root, _, names in os.walk(base):
            if "testdata" in root.replace("\\", "/").split("/"):
                continue
            files += [os.path.join(root, n) for n in names if n.endswith(".go")]
    for i in range(0, len(files), 40):
        subprocess.run(["gofmt", "-w"] + files[i:i + 40], check=True)
