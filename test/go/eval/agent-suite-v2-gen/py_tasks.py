"""Python tasks: 4-file feature, config migration, misleading traceback, long routes audit, big tool output."""
import json

from common import Task, d, cmd, py_filler, dump_json

UNITTEST = ["python", "-m", "unittest", "discover", "-s", "tests", "-t", "."]


# ------------------------------------------------------------ feature: discounts
def discounts():
    t = Task("py-feature-discounts")
    t.fx("invoicer/__init__.py", '"""Tiny invoicing package."""\n')
    models = d('''
        """Invoice data model."""
        from dataclasses import dataclass, field


        @dataclass
        class LineItem:
            sku: str
            qty: int
            unit_cents: int


        @dataclass
        class Invoice:
            number: str
            customer: str
            items: list = field(default_factory=list)
            tax_rate_bp: int = 0  # basis points: 2300 means 23%
        ''')
    t.fx("invoicer/models.py", models)
    pricing = d('''
        """Money arithmetic. All amounts are integer cents."""


        def round_half_up(numer, denom):
            """Integer numer/denom rounded half up (both non-negative)."""
            return (2 * numer + denom) // (2 * denom)


        def subtotal_cents(inv):
            return sum(item.qty * item.unit_cents for item in inv.items)


        def tax_cents(amount_cents, rate_bp):
            return round_half_up(amount_cents * rate_bp, 10000)


        def total_cents(inv):
            sub = subtotal_cents(inv)
            return sub + tax_cents(sub, inv.tax_rate_bp)
        ''')
    t.fx("invoicer/pricing.py", pricing)
    storage = d('''
        """JSON persistence for invoices."""
        import json

        from .models import Invoice, LineItem


        def invoice_to_dict(inv):
            return {
                "number": inv.number,
                "customer": inv.customer,
                "tax_rate_bp": inv.tax_rate_bp,
                "items": [{"sku": i.sku, "qty": i.qty, "unit_cents": i.unit_cents} for i in inv.items],
            }


        def invoice_from_dict(data):
            return Invoice(
                number=data["number"],
                customer=data["customer"],
                tax_rate_bp=data.get("tax_rate_bp", 0),
                items=[LineItem(**i) for i in data.get("items", [])],
            )


        def load_invoice(path):
            with open(path, encoding="utf-8") as f:
                return invoice_from_dict(json.load(f))


        def save_invoice(inv, path):
            with open(path, "w", encoding="utf-8") as f:
                json.dump(invoice_to_dict(inv), f, indent=2)
        ''')
    t.fx("invoicer/storage.py", storage)
    t.fx("invoicer/formatting.py", d('''
        """Human readable money."""


        def fmt_cents(cents):
            sign = "-" if cents < 0 else ""
            cents = abs(cents)
            return "%s%d.%02d" % (sign, cents // 100, cents % 100)
        '''))
    cli = d('''
        """Command line: python -m invoicer.cli total INVOICE.json"""
        import argparse
        import sys

        from .formatting import fmt_cents
        from .pricing import subtotal_cents, tax_cents
        from .storage import load_invoice


        def main(argv=None, out=sys.stdout):
            parser = argparse.ArgumentParser(prog="invoicer")
            sub = parser.add_subparsers(dest="cmd", required=True)
            total = sub.add_parser("total", help="print subtotal, tax and total")
            total.add_argument("invoice")
            args = parser.parse_args(argv)
            if args.cmd == "total":
                inv = load_invoice(args.invoice)
                s = subtotal_cents(inv)
                tax = tax_cents(s, inv.tax_rate_bp)
                print("Subtotal: " + fmt_cents(s), file=out)
                print("Tax: " + fmt_cents(tax), file=out)
                print("Total: " + fmt_cents(s + tax), file=out)
            return 0


        if __name__ == "__main__":
            sys.exit(main())
        ''')
    t.fx("invoicer/cli.py", cli)
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_pricing.py", d('''
        import unittest

        from invoicer.models import Invoice, LineItem
        from invoicer.pricing import round_half_up, total_cents


        class PricingTest(unittest.TestCase):
            def test_round_half_up(self):
                self.assertEqual(round_half_up(5, 10), 1)
                self.assertEqual(round_half_up(4, 10), 0)

            def test_total(self):
                inv = Invoice("1", "acme", [LineItem("a", 2, 1000)], tax_rate_bp=2300)
                self.assertEqual(total_cents(inv), 2460)


        if __name__ == "__main__":
            unittest.main()
        '''))
    t.fx("tests/test_cli.py", d('''
        import io
        import json
        import os
        import tempfile
        import unittest

        from invoicer.cli import main


        class CliTest(unittest.TestCase):
            def test_total(self):
                with tempfile.TemporaryDirectory() as tmp:
                    path = os.path.join(tmp, "inv.json")
                    with open(path, "w") as f:
                        json.dump({"number": "7", "customer": "x", "tax_rate_bp": 1000,
                                   "items": [{"sku": "a", "qty": 1, "unit_cents": 1999}]}, f)
                    out = io.StringIO()
                    main(["total", path], out=out)
                    self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Tax: 2.00", "Total: 21.99"])
        '''))
    spec = d('''
        # Discount codes (spec)

        1. `invoicer.models.Discount` is a dataclass with fields `code: str`,
           `percent: int | None = None`, `amount_cents: int | None = None`,
           `min_subtotal_cents: int = 0`. Exactly one of `percent` and
           `amount_cents` must be set, otherwise `__post_init__` raises
           `ValueError`. `percent` must be between 1 and 100 (inclusive),
           `amount_cents` must be positive; otherwise `ValueError`.
        2. `Invoice` gets a new last field `discount_code: str | None = None`.
        3. `invoicer.pricing.discount_cents(inv, discounts)` returns the discount
           in cents for an invoice, where `discounts` maps codes to `Discount`:
           - no `discount_code` on the invoice -> 0;
           - codes are case-insensitive (`spring10` and `SPRING10` are the same
             code); an unknown code raises `KeyError`;
           - if the subtotal is below `min_subtotal_cents` -> 0;
           - percent discounts are `round_half_up(subtotal * percent, 100)`;
           - amount discounts are capped at the subtotal.
        4. `invoicer.pricing.total_cents(inv, discounts=None)`: the discount is
           subtracted from the subtotal **before** tax; tax is computed on the
           discounted amount. `discounts=None` means no known codes.
        5. Storage: the invoice JSON has an optional `"discount_code"` key
           (omitted when there is no code; files without it still load).
           `invoicer.storage.load_discounts(path)` reads a JSON list of objects
           (`code`, `percent` or `amount_cents`, optional `min_subtotal_cents`)
           and returns a dict mapping each code to its `Discount`.
        6. CLI: `total` accepts `--discounts PATH`. When the discount is
           non-zero, a line `Discount (CODE): -X.XX` (CODE upper-cased) is
           printed between `Subtotal` and `Tax`, and Tax/Total use the
           discounted amount. Output is unchanged for invoices without a code.
        7. Add tests in `tests/test_discounts.py` (at least 4 test methods).
        ''')
    t.fx("docs/DISCOUNTS.md", spec)
    t.fx("README.md", "# invoicer\n\n`python -m invoicer.cli total inv.json`. Tests: `python -m unittest discover -s tests -t .`\n")
    for pkg in ["customers", "reports", "currency", "exporters", "reminders"]:
        py_filler(t, "invoicer/" + pkg, count=4)

    # reference
    t.ref("invoicer/models.py", (models + "\n\n" + d('''


        @dataclass
        class Discount:
            code: str
            percent: int = None
            amount_cents: int = None
            min_subtotal_cents: int = 0

            def __post_init__(self):
                if (self.percent is None) == (self.amount_cents is None):
                    raise ValueError("discount needs exactly one of percent or amount_cents")
                if self.percent is not None and not 1 <= self.percent <= 100:
                    raise ValueError("percent must be 1..100")
                if self.amount_cents is not None and self.amount_cents <= 0:
                    raise ValueError("amount_cents must be positive")
        ''')).replace(
        "    tax_rate_bp: int = 0  # basis points: 2300 means 23%\n", "    tax_rate_bp: int = 0  # basis points: 2300 means 23%\n    discount_code: str = None\n"))
    t.ref("invoicer/pricing.py", pricing.replace(d('''
        def total_cents(inv):
            sub = subtotal_cents(inv)
            return sub + tax_cents(sub, inv.tax_rate_bp)
        '''), d('''
        def discount_cents(inv, discounts):
            code = getattr(inv, "discount_code", None)
            if not code:
                return 0
            table = {k.upper(): v for k, v in (discounts or {}).items()}
            disc = table.get(code.upper())
            if disc is None:
                raise KeyError("unknown discount code: %s" % code)
            sub = subtotal_cents(inv)
            if sub < disc.min_subtotal_cents:
                return 0
            if disc.percent is not None:
                return round_half_up(sub * disc.percent, 100)
            return min(disc.amount_cents, sub)


        def total_cents(inv, discounts=None):
            taxable = subtotal_cents(inv) - discount_cents(inv, discounts)
            return taxable + tax_cents(taxable, inv.tax_rate_bp)
        ''')))
    t.ref("invoicer/storage.py", storage.replace("from .models import Invoice, LineItem", "from .models import Discount, Invoice, LineItem").replace(d('''
            return {
                "number": inv.number,
                "customer": inv.customer,
                "tax_rate_bp": inv.tax_rate_bp,
                "items": [{"sku": i.sku, "qty": i.qty, "unit_cents": i.unit_cents} for i in inv.items],
            }
        ''').replace("\n", "\n    ").rstrip(" ").join(["    ", ""]), d('''
            out = {
                "number": inv.number,
                "customer": inv.customer,
                "tax_rate_bp": inv.tax_rate_bp,
                "items": [{"sku": i.sku, "qty": i.qty, "unit_cents": i.unit_cents} for i in inv.items],
            }
            if inv.discount_code:
                out["discount_code"] = inv.discount_code
            return out
        ''').replace("\n", "\n    ").rstrip(" ").join(["    ", ""])).replace(
        '        items=[LineItem(**i) for i in data.get("items", [])],\n',
        '        items=[LineItem(**i) for i in data.get("items", [])],\n        discount_code=data.get("discount_code"),\n') + d('''


        def load_discounts(path):
            with open(path, encoding="utf-8") as f:
                rows = json.load(f)
            return {r["code"]: Discount(**r) for r in rows}
        '''))
    t.ref("invoicer/cli.py", cli.replace("from .pricing import subtotal_cents, tax_cents", "from .pricing import discount_cents, subtotal_cents, tax_cents")
          .replace("from .storage import load_invoice", "from .storage import load_discounts, load_invoice")
          .replace('    total.add_argument("invoice")\n', '    total.add_argument("invoice")\n    total.add_argument("--discounts")\n')
          .replace(d('''
                    s = subtotal_cents(inv)
                    tax = tax_cents(s, inv.tax_rate_bp)
                    print("Subtotal: " + fmt_cents(s), file=out)
                    print("Tax: " + fmt_cents(tax), file=out)
                    print("Total: " + fmt_cents(s + tax), file=out)
            ''').replace("\n", "\n        ").rstrip(" ").join(["        ", ""]), d('''
                    discounts = load_discounts(args.discounts) if args.discounts else {}
                    s = subtotal_cents(inv)
                    disc = discount_cents(inv, discounts)
                    tax = tax_cents(s - disc, inv.tax_rate_bp)
                    print("Subtotal: " + fmt_cents(s), file=out)
                    if disc:
                        print("Discount (%s): -%s" % (inv.discount_code.upper(), fmt_cents(disc)), file=out)
                    print("Tax: " + fmt_cents(tax), file=out)
                    print("Total: " + fmt_cents(s - disc + tax), file=out)
            ''').replace("\n", "\n        ").rstrip(" ").join(["        ", ""])))
    t.ref("tests/test_discounts.py", d('''
        import unittest

        from invoicer.models import Discount, Invoice, LineItem
        from invoicer.pricing import discount_cents, total_cents


        def inv(code=None, unit=10000):
            return Invoice("1", "c", [LineItem("a", 1, unit)], tax_rate_bp=2300, discount_code=code)


        class DiscountTest(unittest.TestCase):
            def test_validation(self):
                with self.assertRaises(ValueError):
                    Discount("X")
                with self.assertRaises(ValueError):
                    Discount("X", percent=10, amount_cents=5)

            def test_percent(self):
                self.assertEqual(discount_cents(inv("p"), {"P": Discount("P", percent=10)}), 1000)

            def test_amount_capped(self):
                self.assertEqual(discount_cents(inv("a", unit=300), {"A": Discount("A", amount_cents=500)}), 300)

            def test_tax_after_discount(self):
                self.assertEqual(total_cents(inv("p"), {"P": Discount("P", percent=10)}), 11070)

            def test_unknown(self):
                with self.assertRaises(KeyError):
                    total_cents(inv("nope"), {})
        '''))
    t.gr("tests/test_zz_hidden_discounts.py", d('''
        import io
        import json
        import os
        import tempfile
        import unittest

        from invoicer.cli import main
        from invoicer.models import Discount, Invoice, LineItem
        from invoicer.pricing import discount_cents, total_cents
        from invoicer.storage import load_discounts, load_invoice, save_invoice


        def make(code=None, units=(1999, 1), tax=2300):
            return Invoice("9", "c", [LineItem("s", units[1], units[0])], tax_rate_bp=tax, discount_code=code)


        class HiddenDiscounts(unittest.TestCase):
            def test_model_validation(self):
                for kwargs in ({}, {"percent": 5, "amount_cents": 5}, {"percent": 0}, {"percent": 101}, {"amount_cents": 0}, {"amount_cents": -3}):
                    with self.assertRaises(ValueError, msg=str(kwargs)):
                        Discount("X", **kwargs)
                self.assertEqual(Discount("X", percent=100).min_subtotal_cents, 0)
                self.assertIsNone(Invoice("1", "c").discount_code)

            def test_percent_rounding_half_up(self):
                # 1999 * 15% = 299.85 -> 300 ; 1990 * 25% = 497.5 -> 498
                self.assertEqual(discount_cents(make("q15"), {"Q15": Discount("Q15", percent=15)}), 300)
                self.assertEqual(discount_cents(make("q25", (1990, 1)), {"q25": Discount("q25", percent=25)}), 498)

            def test_case_insensitive_and_unknown(self):
                d = {"Spring10": Discount("Spring10", percent=10)}
                self.assertEqual(discount_cents(make("SPRING10", (1000, 1)), d), 100)
                self.assertEqual(discount_cents(make("spring10", (1000, 1)), d), 100)
                with self.assertRaises(KeyError):
                    discount_cents(make("other"), d)
                with self.assertRaises(KeyError):
                    total_cents(make("x"))
                self.assertEqual(discount_cents(make(None), d), 0)

            def test_min_subtotal_and_cap(self):
                d = {"BIG": Discount("BIG", amount_cents=5000, min_subtotal_cents=2000), "CAP": Discount("CAP", amount_cents=999999)}
                self.assertEqual(discount_cents(make("big", (1999, 1)), d), 0)
                self.assertEqual(discount_cents(make("big", (1000, 2)), d), 2000)
                self.assertEqual(discount_cents(make("big", (3000, 2)), d), 5000)
                self.assertEqual(discount_cents(make("cap", (1500, 1)), d), 1500)
                self.assertEqual(total_cents(make("cap", (1500, 1)), d), 0)

            def test_tax_on_discounted_amount(self):
                d = {"P": Discount("P", percent=10)}
                # subtotal 1999, discount 200, taxable 1799, tax 23% = 413.77 -> 414
                self.assertEqual(total_cents(make("p"), d), 1799 + 414)
                self.assertEqual(total_cents(make(None)), 1999 + 460)

            def test_storage_roundtrip_and_legacy(self):
                with tempfile.TemporaryDirectory() as tmp:
                    p = os.path.join(tmp, "i.json")
                    save_invoice(make(None), p)
                    self.assertNotIn("discount_code", json.load(open(p)))
                    save_invoice(make("P"), p)
                    self.assertEqual(load_invoice(p).discount_code, "P")
                    with open(p, "w") as f:
                        json.dump({"number": "1", "customer": "c", "items": []}, f)
                    self.assertIsNone(load_invoice(p).discount_code)
                    dp = os.path.join(tmp, "d.json")
                    with open(dp, "w") as f:
                        json.dump([{"code": "A", "percent": 5}, {"code": "B", "amount_cents": 100, "min_subtotal_cents": 50}], f)
                    ds = load_discounts(dp)
                    self.assertEqual(set(ds), {"A", "B"})
                    self.assertEqual(ds["B"].min_subtotal_cents, 50)
                    self.assertIsInstance(ds["A"], Discount)

            def test_cli(self):
                with tempfile.TemporaryDirectory() as tmp:
                    ip, dp = os.path.join(tmp, "i.json"), os.path.join(tmp, "d.json")
                    save_invoice(make("spring", (1999, 1), 2300), ip)
                    with open(dp, "w") as f:
                        json.dump([{"code": "SPRING", "percent": 10}], f)
                    out = io.StringIO()
                    main(["total", ip, "--discounts", dp], out=out)
                    self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Discount (SPRING): -2.00", "Tax: 4.14", "Total: 22.13"])
                    save_invoice(make(None), ip)
                    out = io.StringIO()
                    main(["total", ip], out=out)
                    self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Tax: 4.60", "Total: 24.59"])
        '''))
    t.gr("graders/check_agent_tests.py", d('''
        import re, sys
        try:
            text = open("tests/test_discounts.py", encoding="utf-8").read()
        except OSError:
            print("tests/test_discounts.py missing"); sys.exit(1)
        n = len(re.findall(r"^\\s+def test_\\w+\\(self", text, re.M))
        print("agent test methods:", n)
        sys.exit(0 if n >= 4 else 1)
        '''))
    t.register(
        name="Implement a discount-code feature across models, pricing, storage, CLI and tests",
        category="feature", tags=["python", "feature", "multi-file", "spec"], difficulty="hard",
        prompt=("Implement the discount-code feature described in docs/DISCOUNTS.md (all 7 points) in the invoicer "
                "package. Existing behaviour for invoices without a discount code must not change and the existing "
                "tests must keep passing. Run `python -m unittest discover -s tests -t .` before you finish."),
        strictDiff=True,
        allowedChangedFiles=["invoicer/models.py", "invoicer/pricing.py", "invoicer/storage.py", "invoicer/cli.py", "invoicer/formatting.py", "tests/test_discounts.py", "README.md", "docs/DISCOUNTS.md"],
        forbiddenChangedFiles=["tests/test_pricing.py", "tests/test_cli.py"],
        expectedChangedFiles=["invoicer/models.py", "invoicer/pricing.py", "invoicer/storage.py", "invoicer/cli.py", "tests/test_discounts.py"],
        verificationCommands=[cmd("unittest", *UNITTEST), cmd("agent-tests", "python", "graders/check_agent_tests.py")],
    )


# ------------------------------------------------------------ config migration
ENVS = {
    "dev": {"name": "api-dev", "timeout_ms": 5000, "db_url": "postgres://localhost:5432/app_dev", "features": "search, export", "log_level": "DEBUG", "region": "local"},
    "test": {"name": "api-test", "timeout_ms": 1500, "db_url": "postgres://db-test/app_test", "features": "search", "log_level": "INFO"},
    "staging": {"name": "api-staging", "timeout_ms": 10000, "db_url": "postgres://db.staging.internal:6432/app", "features": "search,export,,beta", "log_level": "Info", "replicas": 2},
    "prod": {"name": "api-prod", "timeout_ms": 30000, "db_url": "postgres://db.prod.internal:5432/app", "features": "search, export, audit", "log_level": "WARNING", "replicas": 6, "region": "eu-west-1"},
    "prod-us": {"name": "api-prod-us", "timeout_ms": 30000, "db_url": "postgres://db.us.internal:5433/app_us", "features": "search, export, audit, search", "log_level": "warning", "region": "us-east-1"},
    "perf": {"name": "api-perf", "timeout_ms": 250, "db_url": "postgres://perfdb:5432/bench", "features": "", "log_level": "ERROR", "extra": {"profile": True, "samples": [1, 2, 3]}},
    "demo": {"name": "api-demo", "timeout_ms": 60000, "db_url": "postgres://demo-db/demo", "features": " export ", "region": "eu-central-1"},
    "ci": {"name": "api-ci", "timeout_ms": 2000, "db_url": "postgres://127.0.0.1:55432/ci", "features": "beta,search", "log_level": "debug"},
    "sandbox": {"name": "api-sandbox", "timeout_ms": 7500, "db_url": "postgres://sandbox.db.internal/sbx", "features": "search,beta,export,beta", "log_level": "INFO", "owner": "team-sbx"},
    "dr": {"name": "api-dr", "timeout_ms": 45000, "db_url": "postgres://dr-db.internal:5432/app", "features": "search , export", "log_level": "CRITICAL", "replicas": 1, "region": "eu-north-1"},
}


def migrate(v1):
    out = {"schema": 2}
    for k, v in v1.items():
        if k == "timeout_ms":
            out["timeout"] = ("%ds" % (v // 1000)) if v % 1000 == 0 else "%dms" % v
        elif k == "db_url":
            rest = v[len("postgres://"):]
            hostport, name = rest.split("/", 1)
            host, _, port = hostport.partition(":")
            out["db"] = {"host": host, "port": int(port) if port else 5432, "name": name}
        elif k == "features":
            feats = []
            for f in v.split(","):
                f = f.strip()
                if f and f not in feats:
                    feats.append(f)
            out["features"] = feats
        elif k == "log_level":
            out["logging"] = {"level": v.lower()}
        else:
            out[k] = v
    if "logging" not in out:
        out["logging"] = {"level": "info"}
    return out


def config_migration():
    t = Task("py-config-migration")
    for name, cfg in ENVS.items():
        t.fx("config/envs/%s.json" % name, dump_json(cfg))
        t.ref("config/envs/%s.json" % name, dump_json(migrate(cfg)))
        t.gr("graders/expected/%s.json" % name, dump_json(migrate(cfg)))
    loader = d('''
        """Load service settings from config/envs/<env>.json (schema 1)."""
        import json
        from dataclasses import dataclass, field


        @dataclass(frozen=True)
        class Settings:
            name: str
            timeout_ms: int
            db_url: str
            features: tuple = field(default=())
            log_level: str = "info"


        def _normalize_db_url(url):
            prefix = "postgres://"
            hostport, name = url[len(prefix):].split("/", 1)
            if ":" not in hostport:
                hostport += ":5432"
            return prefix + hostport + "/" + name


        def parse(data):
            features = tuple(f.strip() for f in data.get("features", "").split(",") if f.strip())
            dedup = []
            for f in features:
                if f not in dedup:
                    dedup.append(f)
            return Settings(
                name=data["name"],
                timeout_ms=int(data["timeout_ms"]),
                db_url=_normalize_db_url(data["db_url"]),
                features=tuple(dedup),
                log_level=data.get("log_level", "info").lower(),
            )


        def load(path):
            with open(path, encoding="utf-8") as f:
                return parse(json.load(f))
        ''')
    t.fx("app/__init__.py", "")
    t.fx("app/config_loader.py", loader)
    t.fx("app/server.py", d('''
        """Prints the effective settings for an environment."""
        import sys

        from app.config_loader import load


        def describe(env):
            s = load("config/envs/%s.json" % env)
            return "%s timeout=%dms db=%s features=%s log=%s" % (s.name, s.timeout_ms, s.db_url, ",".join(s.features), s.log_level)


        if __name__ == "__main__":
            print(describe(sys.argv[1] if len(sys.argv) > 1 else "dev"))
        '''))
    t.fx("tests/__init__.py", "")
    sample_v1 = {"name": "svc", "timeout_ms": 2500, "db_url": "postgres://h:7000/d", "features": "a, b, a", "log_level": "WARNING"}
    t.fx("tests/fixtures/sample.json", dump_json(sample_v1))
    t.fx("tests/test_loader.py", d('''
        import os
        import unittest

        from app.config_loader import Settings, load

        HERE = os.path.dirname(__file__)


        class LoaderTest(unittest.TestCase):
            def test_sample(self):
                s = load(os.path.join(HERE, "fixtures", "sample.json"))
                self.assertEqual(s, Settings("svc", 2500, "postgres://h:7000/d", ("a", "b"), "warning"))

            def test_all_envs_load(self):
                for name in os.listdir("config/envs"):
                    load(os.path.join("config/envs", name))
        '''))
    t.fx("MIGRATION.md", d('''
        # Config schema 2

        All files in `config/envs/` move from schema 1 to schema 2. The
        loader (`app/config_loader.py`) must read schema 2 only and raise
        `ValueError` (message containing `schema`) for anything else,
        including old schema-1 files. The `Settings` dataclass and the
        values it produces for each environment must stay exactly the same.

        Schema 2 rules (key order does not matter):

        - add `"schema": 2`;
        - `timeout_ms` (int) becomes `timeout` (string): whole seconds are
          written as `"<n>s"` (5000 -> `"5s"`), anything else as
          `"<n>ms"` (1500 -> `"1500ms"`);
        - `db_url` (`postgres://host[:port]/name`) becomes
          `"db": {"host": ..., "port": <int>, "name": ...}`; a missing port
          means 5432 and is written explicitly;
        - `features` (comma separated string) becomes a JSON list: entries
          trimmed, empty entries dropped, duplicates removed (first
          occurrence wins), order kept;
        - `log_level` becomes `"logging": {"level": <lower-cased value>}`;
          when `log_level` is missing write `"logging": {"level": "info"}`;
        - every other key is kept unchanged.

        Migrate the test fixture(s) under `tests/fixtures/` as well.
        '''))
    t.fx("README.md", "# svcconfig\n\nSettings per environment. `python -m unittest discover -s tests -t .`\n")
    for pkg in ["app/metrics", "app/handlers", "app/jobs"]:
        py_filler(t, pkg, count=3)
    # reference loader + fixture
    ref_loader = d('''
        """Load service settings from config/envs/<env>.json (schema 2)."""
        import json
        from dataclasses import dataclass, field


        @dataclass(frozen=True)
        class Settings:
            name: str
            timeout_ms: int
            db_url: str
            features: tuple = field(default=())
            log_level: str = "info"


        def _timeout_ms(text):
            if text.endswith("ms"):
                return int(text[:-2])
            if text.endswith("s"):
                return int(text[:-1]) * 1000
            raise ValueError("bad timeout %r" % text)


        def parse(data):
            if data.get("schema") != 2:
                raise ValueError("unsupported config schema: %r" % data.get("schema", 1))
            db = data["db"]
            return Settings(
                name=data["name"],
                timeout_ms=_timeout_ms(data["timeout"]),
                db_url="postgres://%s:%d/%s" % (db["host"], db["port"], db["name"]),
                features=tuple(data.get("features", [])),
                log_level=data.get("logging", {}).get("level", "info"),
            )


        def load(path):
            with open(path, encoding="utf-8") as f:
                return parse(json.load(f))
        ''')
    t.ref("app/config_loader.py", ref_loader)
    t.ref("tests/fixtures/sample.json", dump_json(migrate(sample_v1)))
    # expected Settings per env from the ORIGINAL loader semantics, written out explicitly
    expected = {}
    for name, cfg in ENVS.items():
        m = migrate(cfg)
        db = m["db"]
        expected[name] = [cfg["name"], cfg["timeout_ms"], "postgres://%s:%d/%s" % (db["host"], db["port"], db["name"]), m["features"], m["logging"]["level"]]
    t.gr("graders/expected_settings.json", dump_json(expected))
    t.gr("tests/test_zz_hidden_migration.py", d('''
        import json
        import os
        import tempfile
        import unittest

        from app.config_loader import load


        class HiddenMigration(unittest.TestCase):
            def test_env_files_exact(self):
                for name in sorted(os.listdir("graders/expected")):
                    want = json.load(open(os.path.join("graders/expected", name), encoding="utf-8"))
                    got = json.load(open(os.path.join("config/envs", name), encoding="utf-8"))
                    self.assertEqual(got, want, name)

            def test_settings_unchanged(self):
                expected = json.load(open("graders/expected_settings.json", encoding="utf-8"))
                for name, (n, t, db, feats, lvl) in expected.items():
                    s = load("config/envs/%s.json" % name)
                    self.assertEqual((s.name, s.timeout_ms, s.db_url, tuple(s.features), s.log_level), (n, t, db, tuple(feats), lvl), name)

            def test_rejects_schema_1(self):
                with tempfile.TemporaryDirectory() as tmp:
                    p = os.path.join(tmp, "old.json")
                    with open(p, "w") as f:
                        json.dump({"name": "x", "timeout_ms": 1, "db_url": "postgres://h/d"}, f)
                    with self.assertRaises(ValueError) as ctx:
                        load(p)
                    self.assertIn("schema", str(ctx.exception))
                    with open(p, "w") as f:
                        json.dump({"schema": 3, "name": "x", "timeout": "1s", "db": {"host": "h", "port": 1, "name": "d"}}, f)
                    with self.assertRaises(ValueError):
                        load(p)

            def test_v2_parsing(self):
                with tempfile.TemporaryDirectory() as tmp:
                    p = os.path.join(tmp, "new.json")
                    with open(p, "w") as f:
                        json.dump({"schema": 2, "name": "n", "timeout": "750ms", "db": {"host": "h", "port": 1234, "name": "d"}, "features": ["x", "y"], "logging": {"level": "error"}}, f)
                    s = load(p)
                    self.assertEqual((s.timeout_ms, s.db_url, tuple(s.features), s.log_level), (750, "postgres://h:1234/d", ("x", "y"), "error"))
        '''))
    t.register(
        name="Migrate 10 JSON configs and their loader to schema 2 without changing effective settings",
        category="migration", tags=["python", "json", "migration", "multi-file"], difficulty="hard",
        prompt=("Migrate this repository's configuration to schema 2 as specified in MIGRATION.md: every file in "
                "config/envs/, the loader in app/config_loader.py and the test fixture(s). The effective Settings for "
                "every environment must not change. Run `python -m unittest discover -s tests -t .` before you finish."),
        strictDiff=True,
        allowedChangedFiles=["config/envs/*.json", "app/config_loader.py", "tests/**", "MIGRATION.md", "README.md"],
        expectedChangedFiles=["app/config_loader.py", "config/envs/prod.json"],
        verificationCommands=[cmd("unittest", *UNITTEST)],
    )


# ------------------------------------------------------------ misleading traceback
def py_trace():
    t = Task("py-misleading-trace")
    t.fx("cache/__init__.py", "")
    writer = d('''
        """Writes cache entries as JSON objects.

        Contract: the output is always strict JSON (``json.loads`` accepts it)
        and keys whose value is ``None`` are omitted.
        """
        import json


        def _dump(obj):
            parts = []
            for key, value in obj.items():
                if value is None:
                    parts.append("")
                    continue
                parts.append('"%s": %s' % (key, json.dumps(value, sort_keys=True)))
            return "{" + ", ".join(parts) + "}"


        def write(path, obj):
            with open(path, "w", encoding="utf-8") as f:
                f.write(_dump(obj))
        ''')
    t.fx("cache/writer.py", writer)
    reader = d('''
        """Reads cache entries.

        Strict on purpose: a malformed cache file must raise ``ValueError`` so
        corruption is never silently accepted (the caller rebuilds the entry).
        """
        import json


        def load(path):
            with open(path, encoding="utf-8") as f:
                text = f.read()
            return json.loads(text)
        ''')
    t.fx("cache/reader.py", reader)
    t.fx("app/__init__.py", "")
    t.fx("app/report.py", d('''
        """Builds a cached usage report."""
        import os

        from cache import reader, writer


        def build(stats, cache_dir):
            entry = {
                "user": stats["user"],
                "requests": stats.get("requests", 0),
                "last_error": stats.get("last_error"),
                "plan": stats.get("plan"),
            }
            path = os.path.join(cache_dir, "%s.json" % stats["user"])
            writer.write(path, entry)
            return reader.load(path)
        '''))
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_report.py", d('''
        import tempfile
        import unittest

        from app.report import build


        class ReportTest(unittest.TestCase):
            def test_full_stats(self):
                with tempfile.TemporaryDirectory() as tmp:
                    got = build({"user": "ann", "requests": 3, "last_error": "timeout", "plan": "pro"}, tmp)
                    self.assertEqual(got, {"user": "ann", "requests": 3, "last_error": "timeout", "plan": "pro"})

            def test_without_plan(self):
                with tempfile.TemporaryDirectory() as tmp:
                    got = build({"user": "bob", "requests": 1, "last_error": "x"}, tmp)
                    self.assertEqual(got, {"user": "bob", "requests": 1, "last_error": "x"})

            def test_clean_user(self):
                with tempfile.TemporaryDirectory() as tmp:
                    got = build({"user": "cid", "requests": 0, "plan": "free"}, tmp)
                    self.assertEqual(got, {"user": "cid", "requests": 0, "plan": "free"})
        '''))
    t.fx("README.md", d('''
        # usage-cache

        Known issue: cache files occasionally get corrupted when two workers
        write the same entry; the reader then fails with a JSONDecodeError.
        '''))
    for pkg in ["app/billing", "app/limits", "cache/backends"]:
        py_filler(t, pkg, count=3)
    def line_of(rel, needle):
        return [i + 1 for i, l in enumerate(t.read_fx(rel).splitlines()) if needle in l][0]
    bad = '{"user": "bob", "requests": 1, "last_error": "x", }'
    try:
        json.loads(bad)
        raise SystemExit("py-misleading-trace: sample unexpectedly valid")
    except json.JSONDecodeError as e:
        err = str(e)
    trace = d('''
        ======================================================================
        ERROR: test_without_plan (tests.test_report.ReportTest.test_without_plan)
        ----------------------------------------------------------------------
        Traceback (most recent call last):
          File "tests/test_report.py", line %d, in test_without_plan
            got = build({"user": "bob", "requests": 1, "last_error": "x"}, tmp)
          File "app/report.py", line %d, in build
            return reader.load(path)
          File "cache/reader.py", line %d, in load
            return json.loads(text)
          File "/usr/lib/python3.12/json/__init__.py", line 346, in loads
            return _default_decoder.decode(s)
          File "/usr/lib/python3.12/json/decoder.py", line 337, in decode
            obj, end = self.raw_decode(s, idx=_w(s, 0).end())
          File "/usr/lib/python3.12/json/decoder.py", line 353, in raw_decode
            obj, end = self.scan_once(s, idx)
        json.decoder.JSONDecodeError: %s
        ''') % (line_of("tests/test_report.py", 'build({"user": "bob"'), line_of("app/report.py", "return reader.load"),
                line_of("cache/reader.py", "return json.loads"), err)
    t.ref("cache/writer.py", writer.replace(d('''
        def _dump(obj):
            parts = []
            for key, value in obj.items():
                if value is None:
                    parts.append("")
                    continue
                parts.append('"%s": %s' % (key, json.dumps(value, sort_keys=True)))
            return "{" + ", ".join(parts) + "}"
        '''), d('''
        def _dump(obj):
            return json.dumps({k: v for k, v in obj.items() if v is not None}, sort_keys=True)
        ''')))
    t.gr("tests/test_zz_hidden_cache.py", d('''
        import json
        import os
        import tempfile
        import unittest

        from cache import reader, writer


        class HiddenCache(unittest.TestCase):
            def roundtrip(self, obj):
                with tempfile.TemporaryDirectory() as tmp:
                    p = os.path.join(tmp, "e.json")
                    writer.write(p, obj)
                    text = open(p, encoding="utf-8").read()
                    return json.loads(text)

            def test_writer_strict_json(self):
                self.assertEqual(self.roundtrip({"a": None, "b": 1, "c": None}), {"b": 1})
                self.assertEqual(self.roundtrip({"a": None}), {})
                self.assertEqual(self.roundtrip({}), {})
                self.assertEqual(self.roundtrip({'say "hi"': "x\\\\y", "n": [1, None]}), {'say "hi"': "x\\\\y", "n": [1, None]})

            def test_reader_stays_strict(self):
                for bad in ('{"a": 1,}', '{"a": 1, , "b": 2}', "{'a': 1}", "{"):
                    with tempfile.TemporaryDirectory() as tmp:
                        p = os.path.join(tmp, "bad.json")
                        with open(p, "w", encoding="utf-8") as f:
                            f.write(bad)
                        with self.assertRaises(ValueError, msg=bad):
                            reader.load(p)
        '''))
    t.register(
        name="Debug a JSONDecodeError whose traceback blames the reader",
        category="debugging", tags=["python", "debugging", "root-cause"], difficulty="hard",
        prompt=("The test suite fails (`python -m unittest discover -s tests -t .`):\n\n```\n" + trace + "```\n\n"
                "Find and fix the root cause. Do not modify the tests. Run the test suite before you finish."),
        strictDiff=True,
        allowedChangedFiles=["cache/*.py", "app/report.py"],
        forbiddenChangedFiles=["tests/**"],
        verificationCommands=[cmd("unittest", *UNITTEST)],
    )


# ------------------------------------------------------------ long: routes audit
KNOWN = ["orders.read", "orders.write", "orders.refund", "invoices.read", "invoices.write", "users.read",
         "users.write", "users.admin", "reports.read", "reports.export", "billing.read", "billing.write",
         "audit.read", "webhooks.manage", "settings.read", "settings.write"]
TYPOS = {7: ("orders.write", "orders.writte"), 12: ("invoices.read", "invoice.read"), 19: ("users.admin", "users.admn"),
         26: ("reports.export", "reports.exportt"), 33: ("webhooks.manage", "webhook.manage"), 41: ("billing.write", "biling.write")}


def routes_long():
    t = Task("py-long-routes")
    rng = t.rng
    resources = ["orders", "invoices", "users", "reports", "billing", "audit", "webhooks", "settings"]
    rows = []
    t.fx("app/__init__.py", "")
    t.fx("app/permissions.py", '"""All permissions known to the gateway."""\n\nKNOWN = [\n' + "".join('    "%s",\n' % p for p in KNOWN) + "]\n")
    used = set()
    for i in range(1, 49):
        res = resources[(i * 5) % len(resources)]
        kind = ["", "/{id}", "/{id}/history", "/search", "/export", "/{id}/notes"][(i * 7) % 6]
        route = "/api/%s%s" % (res, kind)
        method = ["GET", "POST", "PUT", "DELETE"][(i * 3) % 4]
        while (route, method) in used:
            kind_i = rng.randint(0, 99)
            route = "/api/%s/v%d%s" % (res, kind_i, kind)
        used.add((route, method))
        perms_pool = [p for p in KNOWN if p.startswith(res + ".") or p.startswith(res[:-1])] or KNOWN[:1]
        perms = sorted(rng.sample(perms_pool, min(len(perms_pool), rng.randint(1, 2))))
        rows.append([i, route, method, perms])
    # two deliberate conflicts
    rows[30][1], rows[30][2] = rows[4][1], rows[4][2]
    rows[44][1], rows[44][2] = rows[17][1], rows[17][2]
    # inject typos
    for i, (good, bad) in TYPOS.items():
        row = rows[i - 1]
        if good not in row[3]:
            row[3] = sorted(set(row[3][:1] + [good]))
    for i, route, method, perms in rows:
        shown = [TYPOS[i][1] if i in TYPOS and p == TYPOS[i][0] else p for p in perms]
        body = _handler_body(rng, i)
        head = '"""Handler h%02d."""\n\nROUTE = "%s"\nMETHOD = "%s"\nPERMS = [%s]\n\n' % (i, route, method, ", ".join('"%s"' % p for p in shown))
        fixed_head = '"""Handler h%02d."""\n\nROUTE = "%s"\nMETHOD = "%s"\nPERMS = [%s]\n\n' % (i, route, method, ", ".join('"%s"' % p for p in perms))
        t.fx("handlers/h%02d.py" % i, head + body)
        if i in TYPOS:
            t.ref("handlers/h%02d.py" % i, fixed_head + body)
        t.gr("graders/expected/h%02d.py.txt" % i, fixed_head + body)
    t.fx("handlers/__init__.py", "")
    lines = ["# Routes", "", "| route | method | handler | perms |", "|---|---|---|---|"]
    for i, route, method, perms in sorted(rows, key=lambda r: (r[1], r[2], r[0])):
        lines.append("| %s | %s | h%02d | %s |" % (route, method, i, ", ".join(perms)))
    lines += ["", "## Conflicts", ""]
    groups = {}
    for i, route, method, perms in rows:
        groups.setdefault((route, method), []).append(i)
    for (route, method), ids in sorted(groups.items()):
        if len(ids) > 1:
            lines.append("%s %s: %s" % (method, route, ", ".join("h%02d" % x for x in sorted(ids))))
    lines += ["", "END-OF-ROUTES"]
    expected = "\n".join(lines) + "\n"
    t.ref("ROUTES.md", expected)
    t.gr("graders/ROUTES.expected.md", expected)
    t.gr("graders/check_routes.py", d('''
        import os, sys
        problems = []
        def norm(s):
            return "\\n".join(l.rstrip() for l in s.replace("\\r\\n", "\\n").strip().split("\\n"))
        want = norm(open("graders/ROUTES.expected.md", encoding="utf-8").read())
        try:
            got = norm(open("ROUTES.md", encoding="utf-8").read())
        except OSError:
            got = ""
            problems.append("ROUTES.md missing")
        if got and got != want:
            wl, gl = want.split("\\n"), got.split("\\n")
            bad = sum(1 for i in range(max(len(wl), len(gl))) if (wl[i] if i < len(wl) else None) != (gl[i] if i < len(gl) else None))
            problems.append("ROUTES.md differs in %d of %d lines" % (bad, len(wl)))
            for i in range(max(len(wl), len(gl))):
                a = wl[i] if i < len(wl) else None
                b = gl[i] if i < len(gl) else None
                if a != b:
                    problems.append("  first diff at line %d: want %r got %r" % (i + 1, a, b))
                    break
        for name in sorted(os.listdir("graders/expected")):
            rel = "handlers/" + name[:-4]
            want_h = open(os.path.join("graders/expected", name), encoding="utf-8").read().replace("\\r\\n", "\\n")
            got_h = open(rel, encoding="utf-8").read().replace("\\r\\n", "\\n")
            if want_h != got_h:
                problems.append(rel + ": wrong content (typo not fixed, or other changes)")
        for p in problems:
            print(p)
        sys.exit(1 if problems else 0)
        '''))
    t.register(
        name="Audit 48 handler files: fix permission typos and build an exact routes table (forces compaction)",
        category="long", tags=["python", "long", "compaction", "audit"], difficulty="hard",
        prompt=("Audit the 48 request handlers in handlers/ (h01.py .. h48.py). Each declares ROUTE, METHOD and PERMS "
                "at the top. Rules for the whole task:\n"
                "1. Some PERMS entries are misspelled. A misspelled entry is not in app/permissions.py KNOWN but is one "
                "typo (one changed, missing or extra letter) away from exactly one KNOWN permission. Fix each one in "
                "place; change nothing else in the handler files.\n"
                "2. Create ROUTES.md: first line `# Routes`, an empty line, then a Markdown table with the exact header "
                "`| route | method | handler | perms |` and separator `|---|---|---|---|`, one row per handler, e.g. "
                "`| /api/orders | GET | h07 | orders.read, orders.write |` (perms after your typo fixes, in the order "
                "they appear in PERMS, joined by `, `). Rows are sorted by route, then method, then handler.\n"
                "3. After the table: an empty line, `## Conflicts`, an empty line, then one line per (method, route) "
                "pair that is declared by more than one handler, as `METHOD ROUTE: hNN, hMM` (handlers ascending), "
                "sorted by route then method.\n"
                "4. After that an empty line and the last line exactly `END-OF-ROUTES`.\n"
                "Read the handler files themselves; do not guess. You may keep notes in NOTES.md."),
        timeoutSeconds=2400, agentConfig="context_window = 16384\n",
        strictDiff=True,
        allowedChangedFiles=["handlers/h*.py", "ROUTES.md", "NOTES.md"],
        forbiddenChangedFiles=["app/**"],
        expectedChangedFiles=["ROUTES.md"],
        verificationCommands=[cmd("routes", "python", "graders/check_routes.py")],
    )


def _handler_body(rng, i):
    fields = rng.sample(["id", "status", "amount", "owner", "created", "updated", "note", "tags", "region", "plan"], 5)
    lines = ["", "def validate(payload):", '    """Return a list of validation problems."""', "    problems = []"]
    for f in fields:
        lines.append('    if "%s" in payload and payload["%s"] in (None, ""):' % (f, f))
        lines.append('        problems.append("%s must not be empty")' % f)
    lines += ["    return problems", "", "", "def handle(request, store):", '    """Entry point called by the router."""',
              "    payload = request.get(\"json\") or {}", "    problems = validate(payload)", "    if problems:",
              '        return {"status": 400, "errors": problems}']
    for k in range(rng.randint(8, 14)):
        f = rng.choice(fields)
        lines.append('    if payload.get("%s") == "%s-%d":' % (f, f, k))
        lines.append('        store.setdefault("%s", []).append(%d)' % (f, rng.randint(1, 999)))
    lines += ['    return {"status": 200, "handler": "h%02d"}' % i, "", "", "def describe():",
              '    return "%s"' % (" ".join(rng.choice(["fast", "cached", "audited", "legacy", "batched", "paged"]) for _ in range(6)))]
    return "\n".join(lines[1:]) + "\n"


# ------------------------------------------------------------ big tool output
def check_output():
    t = Task("py-noisy-validator")
    rng = t.rng
    rows = []
    bad_ids = {4410: None, 9127: None, 12034: None}  # row positions (1-based, header excluded)
    rid = 1000
    for i in range(1, 15001):
        rid += rng.choice([1, 1, 1, 1, 2, 3])
        qty = rng.randint(1, 20)
        unit = rng.randint(50, 50000)
        disc = rng.choice([0, 0, 0, 5, 10, 15, 25])
        total = (2 * qty * unit * (100 - disc) + 100) // 200
        note = rng.choice(["", "", "", "", "promo", "bulk", "manual"])
        rows.append([rid, qty, unit, disc, total, note])
    good = {}
    for i in bad_ids:
        r = rows[i - 1]
        good[i] = r[4]
        r[4] = r[4] + rng.choice([100, 1, 9, 90, -1])
    header = "id,qty,unit_cents,discount_pct,total_cents,note\n"
    t.fx("data/ledger.csv", header + "".join("%d,%d,%d,%d,%d,%s\n" % tuple(r) for r in rows))
    fixed = [list(r) for r in rows]
    for i, v in good.items():
        fixed[i - 1][4] = v
    t.ref("data/ledger.csv", header + "".join("%d,%d,%d,%d,%d,%s\n" % tuple(r) for r in fixed))
    t.fx("tools/validate.py", d('''
        """Validates data/ledger.csv. Prints one line per row (CI wants the full log)."""
        import csv
        import sys


        def expected_total(qty, unit, disc):
            # round half up of qty * unit * (100 - disc) / 100
            return (2 * qty * unit * (100 - disc) + 100) // 200


        def main():
            failures = 0
            with open("data/ledger.csv", newline="", encoding="utf-8") as f:
                for line, row in enumerate(csv.DictReader(f), start=2):
                    rid = int(row["id"])
                    qty, unit, disc = int(row["qty"]), int(row["unit_cents"]), int(row["discount_pct"])
                    if row["note"] == "manual":
                        print("WARN id=%d manual adjustment noted (informational)" % rid)
                    if int(row["total_cents"]) != expected_total(qty, unit, disc):
                        failures += 1
                        print("FAIL line %d: total_cents mismatch" % line)
                    else:
                        print("ok   id=%d" % rid)
            print("%d rows failed" % failures)
            return 1 if failures else 0


        if __name__ == "__main__":
            sys.exit(main())
        '''))
    t.fx("README.md", "# ledger-data\n\nCI runs `python tools/validate.py`; it must exit 0.\n")
    t.register(
        name="Fix data rows flagged deep inside 15k lines of validator output",
        category="tool-output", tags=["python", "big-output", "data"], difficulty="medium",
        prompt=("CI runs `python tools/validate.py` and it fails. Fix the data so the validator passes. Do not modify "
                "the validator, and change only the values that are actually wrong."),
        strictDiff=True,
        allowedChangedFiles=["data/ledger.csv"],
        forbiddenChangedFiles=["tools/**"],
        expectedChangedFiles=["data/ledger.csv"],
        verificationCommands=[cmd("validate", "python", "tools/validate.py")],
        fileChecks=[{"id": "minimal-data-change", "path": "data/ledger.csv", "maxChangedLines": 6, "critical": True}],
    )


def build():
    discounts()
    config_migration()
    py_trace()
    routes_long()
    check_output()
