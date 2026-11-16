"""Large repository search: find exactly the call sites of one function in a
330+ file Python monorepo full of look-alikes (same name in another module,
methods, aliases, comments, strings, references without a call)."""
from common import GRADER_PRELUDE, Task, cmd, d, py_literal

SERVICES = ["accounts", "invoices", "payments", "ledger", "reports", "notifications", "search", "exports",
            "imports", "audit", "pricing", "catalog", "shipping", "returns", "loyalty", "support"]
NOUNS = ["batch", "cursor", "window", "bucket", "ticket", "quota", "token", "route", "shard", "lease", "draft",
         "folio", "voucher", "manifest", "ledger", "slot", "badge", "channel"]
VERBS = ["load", "build", "merge", "split", "score", "render", "flush", "sync", "check", "plan", "trim", "pack"]


def filler_body(rng, n_funcs):
    out = []
    used = set()
    for _ in range(n_funcs):
        verb, noun = rng.choice(VERBS), rng.choice(NOUNS)
        name = "%s_%s" % (verb, noun)
        while name in used:
            name += "_%d" % rng.randint(2, 9)
        used.add(name)
        k = rng.randint(2, 9)
        style = rng.randint(0, 3)
        if style == 0:
            out.append(d('''
                def %s(items, limit=%d):
                    """%s %s entries, at most limit."""
                    result = []
                    for item in items[:limit]:
                        if item:
                            result.append(item)
                    return result
                ''') % (name, k * 10, verb.capitalize(), noun))
        elif style == 1:
            out.append(d('''
                def %s(record):
                    key = record.get("%s_id")
                    if key is None:
                        raise KeyError("%s_id")
                    return "%%s-%%d" %% (key, %d)
                ''') % (name, noun, noun, k))
        elif style == 2:
            out.append(d('''
                def %s(values, factor=%d):
                    total = 0
                    for v in values:
                        total += v * factor
                    return total // max(len(values), 1)
                ''') % (name, k))
        else:
            out.append(d('''
                def %s(tenant_key, region="eu-central"):
                    """Resolve the %s for a tenant key (not the tenant itself)."""
                    return {"tenant": tenant_key, "region": region, "%s": %d}
                ''') % (name, noun, noun, k))
    return "\n\n".join(out)


def build():
    t = Task("large-repo-callsites")
    rng = t.rng
    files = {}
    truth = []

    files["billing/__init__.py"] = ""
    tenancy = d('''
        """Tenant resolution for billing requests."""
        from billing.cache import TenantCache

        _cache = TenantCache()


        def resolve_tenant(request):
            """Return the Tenant for an incoming request (header or subdomain)."""
            key = request.headers.get("X-Tenant") or request.host.split(".")[0]
            return _cache.get(key)


        def resolve_tenant_id(request):
            """Only the tenant id; cheaper than resolve_tenant() for logging."""
            return request.headers.get("X-Tenant") or request.host.split(".")[0]


        def resolve_tenant_or_default(request, default=None):
            try:
                return resolve_tenant(request)
            except KeyError:
                return default
        ''')
    files["billing/tenancy.py"] = tenancy
    truth.append(("billing/tenancy.py", tenancy.split("\n").index("        return resolve_tenant(request)") + 1))
    files["billing/cache.py"] = d('''
        class TenantCache:
            def __init__(self):
                self._data = {}

            def get(self, key):
                return self._data[key]

            def put(self, key, tenant):
                self._data[key] = tenant
        ''')
    files["legacy/__init__.py"] = ""
    files["legacy/tenancy.py"] = d('''
        """Pre-2024 tenant lookup kept for the batch exporters. Different signature!"""


        def resolve_tenant(account_number, strict=False):
            """Map a legacy account number to a tenant code."""
            code = str(account_number)[:4]
            if strict and not code.isdigit():
                raise ValueError(code)
            return code
        ''')
    files["docs/tenancy.md"] = d('''
        # Tenancy

        Call `billing.tenancy.resolve_tenant(request)` once per request:

            tenant = resolve_tenant(request)

        The legacy exporters still use `legacy.tenancy.resolve_tenant(account_number)`.
        ''')

    # True call-site snippets: (import line, list of call lines)
    def true_variants():
        return [
            ("from billing.tenancy import resolve_tenant", "resolve_tenant({arg})"),
            ("from billing import tenancy", "tenancy.resolve_tenant({arg})"),
            ("import billing.tenancy as bt", "bt.resolve_tenant({arg})"),
            ("from billing.tenancy import resolve_tenant as lookup_tenant", "lookup_tenant({arg})"),
            ("import billing.tenancy", "billing.tenancy.resolve_tenant({arg})"),
            ("from billing.tenancy import resolve_tenant_id, resolve_tenant", "resolve_tenant({arg})"),
        ]

    decoys = [
        # legacy function with the same name
        ("from legacy.tenancy import resolve_tenant",
         ["    code = resolve_tenant(row[\"account\"], strict=True)", "    return resolve_tenant(account_number)"]),
        ("from legacy import tenancy",
         ["    return tenancy.resolve_tenant(row[\"account\"])"]),
        ("from billing.tenancy import resolve_tenant_id",
         ["    tenant_id = resolve_tenant_id(request)", "    log.info(\"tenant %s\", resolve_tenant_id(request))"]),
        ("from billing.tenancy import resolve_tenant_or_default",
         ["    tenant = resolve_tenant_or_default(request, default=GUEST)"]),
        (None, ["    # resolve_tenant(request) used to be called here; now the middleware does it",
                "    log.debug(\"skipping resolve_tenant(request) for health checks\")"]),
        (None, ["    with mock.patch(\"billing.tenancy.resolve_tenant\") as fake:",
                "        fake.return_value = None"]),
        ("from billing.tenancy import resolve_tenant",
         ["    HOOKS.append(resolve_tenant)", "    return functools.partial(resolve_tenant)"]),
        (None, ["    return self.resolve_tenant(request)"]),
        (None, ["    return router.resolve_tenant(request)"]),
        (None, ["    return resolve_tenant(request)  # parameter, see signature"]),
    ]

    all_paths = []
    for svc in SERVICES:
        for sub_ in ("handlers", "jobs", "models", "views", "util"):
            n = rng.randint(3, 5)
            for i in range(n):
                all_paths.append("services/%s/%s/%s_%d.py" % (svc, sub_, rng.choice(NOUNS), i + 1))
    all_paths = sorted(set(all_paths))
    rng.shuffle(all_paths)
    true_paths = sorted(all_paths[:18])
    decoy_paths = sorted(all_paths[18:18 + 2 * len(decoys)])
    filler_paths = sorted(all_paths[18 + 2 * len(decoys):])

    variants = true_variants()
    for idx, path in enumerate(true_paths):
        imp, call = variants[idx % len(variants)]
        head = "import logging\n\n" + imp + "\n\nlog = logging.getLogger(__name__)\n\n\n"
        body = filler_body(rng, rng.randint(1, 3)) + "\n\n\n"
        calls = []
        for c in range(rng.randint(1, 3)):
            fn = "handle_%s_%d" % (rng.choice(NOUNS), c + 1)
            arg = rng.choice(["request", "req", "ctx.request"])
            calls.append(("def %s(%s):\n" % (fn, "ctx" if arg.startswith("ctx") else arg),
                          "    tenant = " + call.format(arg=arg) + "\n",
                          "    log.info(\"tenant %s\", tenant.code)\n    return tenant\n"))
        text = head + body
        for sig, line, rest in calls:
            text += sig
            if rng.random() < 0.5:
                text += "    \"\"\"Handle one %s.\"\"\"\n" % rng.choice(NOUNS)
            lineno = text.count("\n") + 1
            truth.append((path, lineno))
            text += line + rest + "\n\n"
        if idx == 4:
            # Same file also references the function without calling it.
            text += "HOOKS = [%s]\n" % call.split("(")[0]
        files[path] = text.rstrip("\n") + "\n"

    for idx, path in enumerate(decoy_paths):
        imp, lines = decoys[idx % len(decoys)]
        head = "import functools\nimport logging\nfrom unittest import mock\n\n"
        if imp:
            head += imp + "\n"
        head += "\nlog = logging.getLogger(__name__)\nHOOKS = []\nGUEST = None\n\n\n"
        body = filler_body(rng, rng.randint(1, 3)) + "\n\n\n"
        k = idx % len(decoys)
        if k == 7:
            fn = d('''
                class TenantRouter:
                    """Routes requests to shards; has its own resolve_tenant method."""

                    def resolve_tenant(self, request):
                        return request.shard

                    def route(self, request):
                ''')
            text = head + body + fn + "\n".join("    " + l for l in lines) + "\n"
        elif k == 8:
            text = head + body + "def dispatch(router, request):\n" + "\n".join(lines) + "\n"
        elif k == 9:
            text = head + body + "def with_tenant(request, resolve_tenant):\n" + "\n".join(lines) + "\n"
        elif k == 5:
            text = head + body + "def test_without_tenant():\n" + "\n".join(lines) + "\n"
        else:
            arg = "request" if "request" in " ".join(lines) else "row"
            text = head + body + "def process(%s, account_number=None):\n" % arg + "\n".join(lines) + "\n"
        files[path] = text

    for path in filler_paths:
        files[path] = "\"\"\"%s\"\"\"\n\n\n" % path.split("/")[-1][:-3].replace("_", " ") + filler_body(rng, rng.randint(2, 5)) + "\n"

    # package markers, tests, config
    for path in list(files):
        parts = path.split("/")[:-1]
        for i in range(1, len(parts) + 1):
            init = "/".join(parts[:i]) + "/__init__.py"
            if init not in files and parts[0] != "docs":
                files[init] = ""
    test_text = d('''
        import unittest
        from types import SimpleNamespace

        from billing.cache import TenantCache
        from billing import tenancy
        from billing.tenancy import resolve_tenant


        class TenancyTest(unittest.TestCase):
            def setUp(self):
                tenancy._cache = TenantCache()
                tenancy._cache.put("acme", "ACME")

            def test_header(self):
                req = SimpleNamespace(headers={"X-Tenant": "acme"}, host="x.example.com")
                self.assertEqual(resolve_tenant(req), "ACME")

            def test_subdomain(self):
                req = SimpleNamespace(headers={}, host="acme.example.com")
                self.assertEqual(tenancy.resolve_tenant(req), "ACME")
        ''')
    files["tests/__init__.py"] = ""
    files["tests/test_tenancy.py"] = test_text
    lines = test_text.split("\n")
    truth.append(("tests/test_tenancy.py", lines.index('        self.assertEqual(resolve_tenant(req), "ACME")') + 1))
    truth.append(("tests/test_tenancy.py", lines.index('        self.assertEqual(tenancy.resolve_tenant(req), "ACME")') + 1))
    files["README.md"] = "# platform\n\nMonorepo of billing services. See docs/ for architecture notes.\n"
    files["setup.cfg"] = "[metadata]\nname = platform\n\n[options]\npackages = find:\n"

    for rel in sorted(files):
        t.fx(rel, files[rel])
    py_count = sum(1 for p in files if p.endswith(".py"))
    assert py_count >= 300, py_count

    # Verify the truth list against the generated text (each line has exactly one intended call).
    for path, ln in truth:
        line = files[path].split("\n")[ln - 1]
        assert "(" in line and ("resolve_tenant(" in line or "lookup_tenant(" in line), (path, ln, line)
    truth = sorted(set(truth))
    expected = ["%s:%d" % x for x in truth]

    argv = t.grade_script(GRADER_PRELUDE + "EXPECTED = " + py_literal(expected) + "\n" + d(r'''
        import re

        if not check("callsites.txt exists", os.path.exists("callsites.txt")):
            finish()
        with open("callsites.txt", encoding="utf-8-sig") as f:
            raw = f.read()
        got = []
        for line in raw.splitlines():
            line = line.strip().replace("\\", "/")
            if not line or line.startswith("#"):
                continue
            line = re.sub(r"^\./", "", line)
            m = re.match(r"^(.+?\.py):(\d+)\b", line)
            got.append("%s:%d" % (m.group(1), int(m.group(2))) if m else line)
        want, have = set(EXPECTED), set(got)
        missing, extra = sorted(want - have), sorted(have - want)
        check("no missing call sites (%d expected)" % len(want), not missing, "%d missing, e.g. %s" % (len(missing), missing[:4]))
        check("no false positives", not extra, "%d extra, e.g. %s" % (len(extra), extra[:4]))
        check("one entry per line, no duplicates", len(got) == len(have), len(got) - len(have))
        key = lambda s: (s.rsplit(":", 1)[0], int(s.rsplit(":", 1)[1]) if s.rsplit(":", 1)[-1].isdigit() else 0)
        check("sorted by path then line", got == sorted(got, key=key))
        if missing or extra:
            tp = len(want & have)
            print("EVAL_SCORE=%.3f" % (tp / float(len(want | have))))
            sys.exit(1)
        finish()
        '''))
    t.solve_files({"callsites.txt": "".join(e + "\n" for e in expected)}, answer=(
        "Wrote callsites.txt with the %d call sites of `billing.tenancy.resolve_tenant` (direct imports, the "
        "`tenancy.` / `bt.` / `billing.tenancy.` module forms, the `lookup_tenant` alias, the internal call in "
        "billing/tenancy.py and the two in tests). Excluded legacy.tenancy.resolve_tenant, resolve_tenant_id, "
        "resolve_tenant_or_default, methods named resolve_tenant, a parameter named resolve_tenant, comments, "
        "strings, mock.patch targets and references without a call." % len(expected)))
    t.register(
        name="Find every call site of one function in a 330-file repo full of look-alikes",
        description=("Search accuracy at scale: %d true call sites (direct, module-qualified, aliased, in the "
                     "defining module and in tests) among %d Python files with decoys - a same-named function in "
                     "another module, *_id and *_or_default variants, methods, a parameter, comments, strings, "
                     "mock.patch targets and uncalled references. Graded by exact set comparison." % (len(expected), py_count)),
        category="search",
        tags=["search", "large-repo", "python", "decoys", "exact-answer"],
        difficulty="hard",
        prompt=("I need the exact list of places that CALL the function `resolve_tenant` defined in `billing/tenancy.py` "
                "(we're about to change its signature). Count calls through any import style: "
                "`from billing.tenancy import resolve_tenant`, aliases (`... import resolve_tenant as x`), and module-qualified "
                "forms like `tenancy.resolve_tenant(...)` when `tenancy` is `billing.tenancy`. Include calls inside "
                "`billing/tenancy.py` itself and in tests. Do NOT count: the definition, import lines, references that "
                "are not calls, comments, strings, `.md` files, other functions with similar names, methods named "
                "`resolve_tenant`, a parameter that happens to be named `resolve_tenant`, or `legacy.tenancy.resolve_tenant` (a different function with the same name).\n"
                "Write the result to `callsites.txt` in the repo root: one `path:line` per call (POSIX slashes, path "
                "relative to the repo root, 1-based line of the call), sorted by path and then line number. "
                "Don't modify any other file and don't leave helper scripts in the repo."),
        expectedChangedFiles=["callsites.txt"],
        strictDiff=True,
        allowedChangedFiles=["callsites.txt"],
        verificationCommands=[cmd("exact-callsites", *argv)],
    )
