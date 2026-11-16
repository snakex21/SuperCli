"""Git workflows: a merge with conflicts on a new release branch, and a rebase
question that must be answered without touching history.

The repositories are built by .ho3setup/setup.py (see common.SETUP_PY) with
fixed identities and dates, so commit hashes are reproducible and the
graders hard-code them."""
import subprocess

import common
from common import GRADER_PRELUDE, Task, cmd, d, git_history, py_literal, sub

UNITTEST = ["python", "-m", "unittest", "discover", "-s", "tests", "-t", "."]


def replay(steps, final):
    """File trees per branch after the steps (mirrors setup.py)."""
    trees = {"main": {}}
    head = "main"
    for s in steps:
        if s["op"] == "commit":
            tree = trees[head]
            tree.update(s.get("write", {}))
            for p in s.get("delete", []):
                del tree[p]
        elif s["op"] == "branch":
            trees[s["name"]] = dict(trees[s["from"]])
        elif s["op"] == "checkout":
            head = s["name"]
    return trees[final]


def commit(date, message, **write):
    files = {k.replace("__", "/").replace("_DOT_", "."): v for k, v in write.items()}
    return {"op": "commit", "date": date, "message": message, "write": files}


GIT_PRELUDE = d(r'''
    import subprocess


    def git(*args):
        r = subprocess.run(["git"] + list(args), capture_output=True, text=True, encoding="utf-8", errors="replace")
        return r.returncode, r.stdout.strip(), r.stderr.strip()


    def rev(ref):
        code, out, _ = git("rev-parse", "-q", "--verify", ref + "^{commit}")
        return out if code == 0 else None


    def branches():
        return sorted(git("for-each-ref", "--format=%(refname:short)", "refs/heads")[1].split())
    ''')

# ---------------------------------------------------------------- merge

CORE_V1 = d('''
    """Order pricing. Amounts are Decimal values in PLN."""
    from decimal import ROUND_HALF_UP, Decimal

    CENT = Decimal("0.01")


    def money(x):
        """Round to grosze, half up."""
        return Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)


    def order_total(lines):
        """Total of (unit_price, qty) lines, rounded to grosze."""
        total = Decimal("0")
        for unit_price, qty in lines:
            total += Decimal(unit_price) * qty
        return money(total)
    ''')

CORE_FEATURE = d('''
    """Order pricing. Amounts are Decimal values in PLN."""
    from decimal import ROUND_HALF_UP, Decimal

    CENT = Decimal("0.01")

    # (minimum quantity on one line, discount rate), highest tier first.
    DISCOUNT_TIERS = [(100, Decimal("0.10")), (50, Decimal("0.05"))]


    def money(x):
        """Round to grosze, half up."""
        return Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)


    def discount_for(qty):
        """Volume discount rate for one order line."""
        for min_qty, rate in DISCOUNT_TIERS:
            if qty >= min_qty:
                return rate
        return Decimal("0")


    def order_total(lines):
        """Total of (unit_price, qty) lines after volume discounts.

        The discount applies to the net line amount; every line is rounded to grosze.
        """
        total = Decimal("0")
        for unit_price, qty in lines:
            net = Decimal(unit_price) * qty
            total += money(net * (1 - discount_for(qty)))
        return money(total)
    ''')

CORE_MAIN = d('''
    """Order pricing. Amounts are Decimal values in PLN."""
    from decimal import ROUND_HALF_UP, Decimal

    CENT = Decimal("0.01")


    def money(x):
        """Round to grosze, half up."""
        return Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)


    def order_total(lines, vat_rate=Decimal("0")):
        """Gross total of (unit_price, qty) lines, rounded to grosze.

        VAT is computed per line on the net line amount and rounded per line.
        """
        total = Decimal("0")
        for unit_price, qty in lines:
            net = money(Decimal(unit_price) * qty)
            total += money(net * (1 + Decimal(vat_rate)))
        return money(total)
    ''')

CORE_MERGED = d('''
    """Order pricing. Amounts are Decimal values in PLN."""
    from decimal import ROUND_HALF_UP, Decimal

    CENT = Decimal("0.01")

    # (minimum quantity on one line, discount rate), highest tier first.
    DISCOUNT_TIERS = [(100, Decimal("0.10")), (50, Decimal("0.05"))]


    def money(x):
        """Round to grosze, half up."""
        return Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)


    def discount_for(qty):
        """Volume discount rate for one order line."""
        for min_qty, rate in DISCOUNT_TIERS:
            if qty >= min_qty:
                return rate
        return Decimal("0")


    def order_total(lines, vat_rate=Decimal("0")):
        """Gross total of (unit_price, qty) lines after volume discounts.

        The discount applies to the net line amount, VAT is computed on the
        discounted net amount; every line is rounded after each step.
        """
        total = Decimal("0")
        for unit_price, qty in lines:
            net = money(Decimal(unit_price) * qty * (1 - discount_for(qty)))
            total += money(net * (1 + Decimal(vat_rate)))
        return money(total)
    ''')

CHANGELOG_V1 = "# Changelog\n\n## Unreleased\n\n## 1.3.0\n\n- First public release of the pricing module.\n"
CHANGELOG_FEATURE = sub(CHANGELOG_V1, ("## Unreleased\n\n", "## Unreleased\n\n- Volume discounts: 5% from 50 units, 10% from 100 units per line.\n\n"))
CHANGELOG_MAIN = sub(CHANGELOG_V1, ("## Unreleased\n\n", "## Unreleased\n\n- `order_total` takes `vat_rate`; VAT is computed per line.\n\n"))

TEST_CORE_V1 = d('''
    import unittest
    from decimal import Decimal

    from pricing.core import money, order_total


    class CoreTest(unittest.TestCase):
        def test_money_rounds_half_up(self):
            self.assertEqual(money("2.345"), Decimal("2.35"))

        def test_order_total(self):
            self.assertEqual(order_total([("19.99", 3), ("0.50", 4)]), Decimal("61.97"))


    if __name__ == "__main__":
        unittest.main()
    ''')

TEST_CORE_MAIN = sub(TEST_CORE_V1, ('''        self.assertEqual(order_total([("19.99", 3), ("0.50", 4)]), Decimal("61.97"))
''', '''        self.assertEqual(order_total([("19.99", 3), ("0.50", 4)]), Decimal("61.97"))

    def test_vat_per_line(self):
        # 3 x 3.33 = 9.99 -> 12.29 gross; 1 x 0.05 -> 0.06 gross
        self.assertEqual(order_total([("3.33", 3), ("0.05", 1)], vat_rate=Decimal("0.23")), Decimal("12.35"))
'''))

TEST_DISCOUNTS = d('''
    import unittest
    from decimal import Decimal

    from pricing.core import discount_for, order_total


    class DiscountTest(unittest.TestCase):
        def test_tiers(self):
            self.assertEqual(discount_for(49), Decimal("0"))
            self.assertEqual(discount_for(50), Decimal("0.05"))
            self.assertEqual(discount_for(100), Decimal("0.10"))

        def test_total_with_discount(self):
            # 120 x 2.00 = 240.00 - 10% = 216.00; 10 x 1.00 = 10.00
            self.assertEqual(order_total([("2.00", 120), ("1.00", 10)]), Decimal("226.00"))


    if __name__ == "__main__":
        unittest.main()
    ''')

README_V1 = "# pricing\n\nOrder pricing helpers. Tests: `python -m unittest discover -s tests -t .`\n"
README_FEATURE = README_V1 + "\n## Volume discounts\n\nPer order line: 5% from 50 units, 10% from 100 units.\n"


def build_merge():
    t = Task("git-merge-release-branch")
    base = {"pricing/__init__.py": "", "pricing/core.py": CORE_V1, "CHANGELOG.md": CHANGELOG_V1,
            "tests/__init__.py": "", "tests/test_core.py": TEST_CORE_V1, "README.md": README_V1,
            ".gitignore": "__pycache__/\n*.pyc\n"}
    steps = [
        {"op": "commit", "date": "2026-02-02T09:00:00+01:00", "message": "Initial import of the pricing module", "write": base},
        {"op": "branch", "name": "feature/volume-discounts", "from": "main"},
        {"op": "checkout", "name": "feature/volume-discounts"},
        {"op": "commit", "date": "2026-02-10T10:15:00+01:00", "message": "pricing: add volume discount tiers",
         "write": {"pricing/core.py": CORE_FEATURE, "tests/test_discounts.py": TEST_DISCOUNTS, "CHANGELOG.md": CHANGELOG_FEATURE}},
        {"op": "commit", "date": "2026-02-11T08:40:00+01:00", "message": "docs: describe discount tiers",
         "write": {"README.md": README_FEATURE}},
        {"op": "checkout", "name": "main"},
        {"op": "commit", "date": "2026-02-12T14:05:00+01:00", "message": "pricing: add per-line VAT",
         "write": {"pricing/core.py": CORE_MAIN, "tests/test_core.py": TEST_CORE_MAIN, "CHANGELOG.md": CHANGELOG_MAIN}},
    ]
    history = git_history(steps, final="main")
    for rel, c in sorted(replay(steps, "main").items()):
        t.fx(rel, c)
    t.setup_history(history)
    refs, subjects = common.verify_setup_matches_fixture(t)
    main_h = refs["refs/heads/main"]
    feat_h = refs["refs/heads/feature/volume-discounts"]

    expected = {"main": main_h, "feature": feat_h,
                "changelog": ["- Volume discounts: 5% from 50 units, 10% from 100 units per line.",
                              "- `order_total` takes `vat_rate`; VAT is computed per line."]}
    argv = t.grade_script(GRADER_PRELUDE + GIT_PRELUDE + "EXP = " + py_literal(expected) + "\n" + d(r'''
        import random
        from decimal import ROUND_HALF_UP, Decimal

        check("main untouched", rev("refs/heads/main") == EXP["main"], rev("refs/heads/main"))
        check("feature/volume-discounts untouched", rev("refs/heads/feature/volume-discounts") == EXP["feature"])
        rel = rev("refs/heads/release/1.4")
        check("release/1.4 exists", rel is not None, branches())
        code, head, _ = git("symbolic-ref", "-q", "HEAD")
        check("release/1.4 checked out", head == "refs/heads/release/1.4", head)
        if rel:
            code, out, _ = git("rev-list", "--parents", "release/1.4", "^" + EXP["main"], "^" + EXP["feature"])
            merges = [l.split()[1:] for l in out.splitlines() if len(l.split()) > 2]
            check("a merge commit joins main and the feature branch",
                  any(sorted(p) == sorted([EXP["main"], EXP["feature"]]) for p in merges), merges)
            check("no extra merges / at most 3 new commits", len(out.splitlines()) <= 3 and len(merges) == 1, out)
            code, out, _ = git("grep", "-n", "-E", r"^(<<<<<<<|>>>>>>>|=======$)", "release/1.4", "--", ".")
            check("no conflict markers committed", code == 1 and not out, out[:300])
        code, out, _ = git("status", "--porcelain", "--untracked-files=no")
        check("working tree clean", not out, out[:300])
        for p in ("MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD"):
            check("no operation in progress: " + p, not os.path.exists(os.path.join(".git", p)))

        with open("CHANGELOG.md", encoding="utf-8") as f:
            cl = f.read()
        for line in EXP["changelog"]:
            check("changelog keeps: " + line[:30], line in cl)
        check("changelog entries under Unreleased",
              all(cl.find(l) > cl.find("## Unreleased") and cl.find(l) < cl.find("## 1.3.0") for l in EXP["changelog"]))

        sys.path.insert(0, os.getcwd())
        try:
            from pricing.core import order_total
        except Exception as e:  # noqa: BLE001
            check("pricing.core imports", False, e)
            finish()
        CENT = Decimal("0.01")
        m = lambda x: Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)

        def want(lines, vat):
            total = Decimal("0")
            for p, q in lines:
                rate = Decimal("0.10") if q >= 100 else Decimal("0.05") if q >= 50 else Decimal("0")
                net = m(Decimal(p) * q * (1 - rate))
                total += m(net * (1 + Decimal(vat)))
            return m(total)

        rng = random.Random(14)
        cases = [([("19.99", 120), ("5.00", 60), ("3.33", 7)], "0.23"), ([("2.00", 120)], "0"), ([("0.05", 1)], "0.08")]
        for _ in range(40):
            lines = [("%d.%02d" % (rng.randint(0, 80), rng.randint(0, 99)), rng.choice([1, 3, 49, 50, 51, 99, 100, 150]))
                     for _ in range(rng.randint(1, 4))]
            cases.append((lines, rng.choice(["0", "0.05", "0.08", "0.23"])))
        bad = []
        for lines, vat in cases:
            try:
                got = order_total(lines, vat_rate=Decimal(vat))
            except Exception as e:  # noqa: BLE001
                got = repr(e)
            if got != want(lines, vat):
                bad.append((lines, vat, str(got), str(want(lines, vat))))
        check("discount then VAT, both per line", not bad, bad[:2])
        check("default vat_rate still 0", order_total([("2.00", 120)]) == Decimal("216.00"))
        finish()
        '''))
    t.solve(common.SOLVE_PRELUDE + "CORE = " + py_literal(CORE_MERGED) + "\nCHANGELOG = " + py_literal(
        sub(CHANGELOG_V1, ("## Unreleased\n\n", "## Unreleased\n\n" + expected["changelog"][1] + "\n" + expected["changelog"][0] + "\n\n"))) + "\n" + d(r'''
        import subprocess


        def git(*args, ok=(0,)):
            r = subprocess.run(["git"] + list(args), capture_output=True, text=True)
            if r.returncode not in ok:
                sys.exit("git %s: %s %s" % (args, r.stdout, r.stderr))
            return r.returncode


        git("checkout", "-q", "-b", "release/1.4", "main")
        code = git("merge", "--no-ff", "--no-edit", "feature/volume-discounts", ok=(0, 1))
        if code == 0:
            sys.exit("expected a conflict")
        wb("pricing/core.py", CORE)
        wb("CHANGELOG.md", CHANGELOG)
        git("add", "pricing/core.py", "CHANGELOG.md")
        git("commit", "-q", "--no-edit")
        print("Created `release/1.4` from `main` and merged `feature/volume-discounts` into it with a merge commit. "
              "Resolved the conflicts in `pricing/core.py` (volume discount on the net line amount, then per-line VAT "
              "on the discounted amount, rounding after each step) and `CHANGELOG.md` (kept both entries). "
              "`main` and the feature branch are unchanged; all tests pass.")
        '''))
    t.register(
        name="Merge a feature branch into a new release branch and resolve code + changelog conflicts",
        description=("Real git history (built by setup, deterministic hashes). The agent must create release/1.4 "
                     "from main, merge (not rebase) the feature branch, resolve a semantic conflict in order_total "
                     "and a CHANGELOG conflict, and leave main/feature untouched. Graded through git plumbing and "
                     "a property test of the merged pricing rule."),
        category="git",
        tags=["git", "merge", "conflict-resolution", "branch", "python"],
        difficulty="hard",
        prompt=("Please integrate `feature/volume-discounts` with the current `main` on a NEW branch `release/1.4`: "
                "create it from `main` and merge the feature branch into it with a real merge commit (no rebase, no squash). "
                "Leave `main` and `feature/volume-discounts` exactly where they are.\n"
                "Resolve the conflicts so both features work together: the volume discount applies to the net line amount, "
                "and VAT is then computed on the discounted net amount (round to grosze per line after the discount and again "
                "after VAT). Keep both changelog entries under \"Unreleased\". All tests must pass. Commit the merge and leave "
                "`release/1.4` checked out with a clean working tree."),
        expectedChangedFiles=["pricing/core.py"],
        strictDiff=True,
        allowedChangedFiles=["pricing/**", "tests/**", "CHANGELOG.md", "README.md"],
        verificationCommands=[
            cmd("unit-tests", *UNITTEST),
            cmd("git-and-behaviour", *argv, weight=3),
        ],
    )


# ---------------------------------------------------------------- rebase question

REPORT_V1 = d('''
    """Sales report: totals per product."""
    from collections import defaultdict

    from src.fmt import amount


    def totals(rows):
        """rows: dicts with product, region, value -> {product: total}."""
        out = defaultdict(float)
        for r in rows:
            out[r["product"]] += r["value"]
        return dict(out)


    def render(rows):
        lines = []
        for product, value in sorted(totals(rows).items()):
            lines.append("%-12s %12s" % (product, amount(value)))
        return "\\n".join(lines)
    ''')

REPORT_FEATURE = sub(REPORT_V1,
                     ('''def totals(rows):
    """rows: dicts with product, region, value -> {product: total}."""
    out = defaultdict(float)
    for r in rows:
        out[r["product"]] += r["value"]
    return dict(out)
''', '''def totals(rows, by="product"):
    """rows: dicts with product, region, value -> {key: total} grouped by `by`."""
    out = defaultdict(float)
    for r in rows:
        out[r[by]] += r["value"]
    return dict(out)
'''),
                     ('''def render(rows):
    lines = []
    for product, value in sorted(totals(rows).items()):
        lines.append("%-12s %12s" % (product, amount(value)))
''', '''def render(rows, by="product"):
    lines = []
    for key, value in sorted(totals(rows, by).items()):
        lines.append("%-12s %12s" % (key, amount(value)))
'''))

REPORT_MAIN = sub(REPORT_V1,
                  ('''def render(rows):
    lines = []
    for product, value in sorted(totals(rows).items()):
        lines.append("%-12s %12s" % (product, amount(value)))
''', '''def render(rows):
    lines = []
    for product, value in sorted(totals(rows).items()):
        lines.append("%-12s %16s" % (product, amount(value, currency="PLN")))
'''))

FMT_V1 = d('''
    """Number formatting."""


    def amount(value, currency=""):
        text = "%.2f" % value
        return (text + " " + currency).strip()
    ''')

FMT_MAIN = d('''
    """Number formatting."""


    def amount(value, currency=""):
        text = "{:,.2f}".format(value).replace(",", " ")
        return (text + " " + currency).strip()
    ''')

TEST_REPORT = d('''
    import unittest

    from src.report import totals

    ROWS = [{"product": "kawa", "region": "PL-MA", "value": 10.0},
            {"product": "kawa", "region": "PL-MZ", "value": 5.5},
            {"product": "herbata", "region": "PL-MA", "value": 2.0}]


    class ReportTest(unittest.TestCase):
        def test_totals(self):
            self.assertEqual(totals(ROWS), {"kawa": 15.5, "herbata": 2.0})


    if __name__ == "__main__":
        unittest.main()
    ''')

TEST_REGIONS = d('''
    import unittest

    from src.report import totals
    from tests.test_report import ROWS


    class RegionTest(unittest.TestCase):
        def test_by_region(self):
            self.assertEqual(totals(ROWS, by="region"), {"PL-MA": 12.0, "PL-MZ": 5.5})


    if __name__ == "__main__":
        unittest.main()
    ''')

DOC_REPORT = "# Report columns\n\n- `product` - product name\n- `region` - ISO 3166-2 region code\n- `value` - net sales in PLN\n"


def build_rebase_question():
    t = Task("git-rebase-advice")
    base = {"src/__init__.py": "", "src/report.py": REPORT_V1, "src/fmt.py": FMT_V1, "tests/__init__.py": "",
            "tests/test_report.py": TEST_REPORT, "README.md": "# sales-report\n\nRenders sales totals.\n",
            ".gitignore": "__pycache__/\n*.pyc\n"}
    steps = [
        {"op": "commit", "date": "2026-01-05T09:00:00+01:00", "message": "Initial report generator", "write": base},
        {"op": "branch", "name": "feature/report", "from": "main"},
        {"op": "checkout", "name": "feature/report"},
        {"op": "commit", "date": "2026-01-08T11:00:00+01:00", "message": "docs: describe report columns",
         "write": {"docs/report.md": DOC_REPORT}},
        {"op": "commit", "date": "2026-01-09T15:30:00+01:00", "message": "report: group totals by region",
         "write": {"src/report.py": REPORT_FEATURE}},
        {"op": "commit", "date": "2026-01-10T10:10:00+01:00", "message": "tests: cover regional grouping",
         "write": {"tests/test_regions.py": TEST_REGIONS}},
        {"op": "checkout", "name": "main"},
        {"op": "commit", "date": "2026-01-12T09:45:00+01:00", "message": "fmt: thousands separator",
         "write": {"src/fmt.py": FMT_MAIN}},
        {"op": "commit", "date": "2026-01-14T16:20:00+01:00", "message": "report: switch currency formatting to PLN",
         "write": {"src/report.py": REPORT_MAIN}},
    ]
    history = git_history(steps, final="feature/report")
    for rel, c in sorted(replay(steps, "feature/report").items()):
        t.fx(rel, c)
    t.setup_history(history)
    refs, subjects = common.verify_setup_matches_fixture(t)
    conflict_hash = subjects["report: group totals by region"]
    verify_rebase_conflict(t, conflict_hash)
    expected = {"main": refs["refs/heads/main"], "feature": refs["refs/heads/feature/report"]}
    argv = t.grade_script(GRADER_PRELUDE + GIT_PRELUDE + "EXP = " + py_literal(expected) + "\n" + d(r'''
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
        '''))
    t.solve(common.SOLVE_PRELUDE + d(r'''
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
        '''))
    t.register(
        name="Answer a rebase question about a real branch without changing any history",
        description=("Built git history: feature/report is 3 commits ahead of an older main, one of which "
                     "conflicts with main. The agent must report the commits, the conflicting commit and file, and "
                     "the commands, while leaving every ref, HEAD, index and stash untouched (hashes checked)."),
        category="git",
        tags=["git", "rebase", "read-only", "history-preservation"],
        difficulty="medium",
        prompt=("I'm on `feature/report`, which is behind `main`. Before I rebase it onto `main` myself, tell me:\n"
                "1. how many commits `feature/report` has that `main` doesn't, with their subjects;\n"
                "2. which of those commits will hit a conflict during `git rebase main`, and in which file;\n"
                "3. the exact command(s) you would run.\n"
                "Do NOT rebase, merge, reset, commit or otherwise change any branch or commit - I want to review first. "
                "Leave the repository exactly as it is (inspect it any way you like as long as nothing changes)."),
        strictDiff=True,
        allowedChangedFiles=[],
        verificationCommands=[cmd("history-intact", *argv, weight=2)],
        answerChecks=[
            {"id": "commit-count", "anyOf": [r"\b(3|three)\b[^.\n]{0,40}commits?", r"commits?[^.\n]{0,40}\b(3|three)\b"],
             "regex": True, "critical": True},
            {"id": "lists-subjects", "allOf": ["describe report columns", "group totals by region", "cover regional grouping"],
             "weight": 2},
            {"id": "conflict-commit-and-file", "allOf": ["src/report.py"],
             "anyOf": ["group totals by region", conflict_hash[:7]], "critical": True, "weight": 2},
            {"id": "rebase-command", "anyOf": [r"git rebase (-i |--interactive |--onto \S+ )?(origin/)?main\b"], "regex": True},
        ],
    )


def verify_rebase_conflict(task, conflict_hash):
    """Sanity check of the scenario: `git rebase main` stops exactly at the
    regional-grouping commit with a conflict in src/report.py."""
    tmp, ws, _ = common.run_setup_in_temp(task)
    try:
        r = subprocess.run(["git", "-c", "core.autocrlf=false", "-c", "user.name=x", "-c", "user.email=x@y",
                            "rebase", "main"], cwd=ws, capture_output=True, text=True)
        stopped = subprocess.run(["git", "rev-parse", "REBASE_HEAD"], cwd=ws, capture_output=True, text=True).stdout.strip()
        unmerged = subprocess.run(["git", "diff", "--name-only", "--diff-filter=U"], cwd=ws, capture_output=True,
                                  text=True).stdout.split()
        if r.returncode == 0 or stopped != conflict_hash or unmerged != ["src/report.py"]:
            raise SystemExit("%s: unexpected rebase outcome: rc=%d stopped=%s unmerged=%s\n%s" % (
                task.id, r.returncode, stopped, unmerged, r.stdout + r.stderr))
    finally:
        common.rmtree(tmp)


def build():
    build_merge()
    build_rebase_question()
