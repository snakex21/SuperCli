"""Python multi-file rename with string-keyed usages and substring decoys."""
import os
import re

from common import Task, d, cmd, py_filler, dump_json

UNITTEST = ["python", "-m", "unittest", "discover", "-s", "tests", "-t", "."]
OLD = "normalize_sku"
NEW = "canonical_sku"
WORD = re.compile(r"\bnormalize_sku\b")
DECOYS = ["denormalize_sku", "normalize_sku_prefix", "normalize_skus"]

FILES = {
    "catalog/__init__.py": '''
        """Product catalog ingestion: SKU helpers, transforms and pipelines."""

        __version__ = "4.2.0"
        ''',
    "catalog/sku.py": '''
        """SKU helpers.

        normalize_sku() turns a raw vendor SKU into the canonical form used as the
        catalog key. denormalize_sku() renders a canonical SKU back into the vendor
        layout for exports, normalize_sku_prefix() canonicalizes search prefixes and
        normalize_skus() is the batch variant used by the feed importer.
        """

        import re

        _SEP = re.compile(r"[\\s_./]+")


        def normalize_sku(raw):
            """Return the canonical SKU: upper case, '-' separated, no padding zeros."""
            if raw is None:
                raise ValueError("normalize_sku: empty SKU")
            text = _SEP.sub("-", str(raw).strip().upper()).strip("-")
            if not text:
                raise ValueError("normalize_sku: empty SKU")
            parts = [p.lstrip("0") or "0" if p.isdigit() else p for p in text.split("-")]
            return "-".join(p for p in parts if p)


        def normalize_skus(raws):
            """Batch variant of normalize_sku that drops duplicates, keeping order."""
            seen = set()
            out = []
            for raw in raws:
                sku = normalize_sku(raw)
                if sku not in seen:
                    seen.add(sku)
                    out.append(sku)
            return out


        def denormalize_sku(sku, sep="_"):
            """Render a canonical SKU in the vendor layout (default separator '_')."""
            return sep.join(sku.lower().split("-"))


        def normalize_sku_prefix(prefix):
            """Canonicalize a search prefix; a trailing separator is kept."""
            trailing = prefix[-1:] in ("-", "_", ".", "/", " ")
            core = normalize_sku(prefix) if prefix.strip(" -_./") else ""
            return core + ("-" if trailing and core else "")
        ''',
    "catalog/transforms.py": '''
        """Named record transforms.

        Pipelines reference transforms by name (see pipelines/*.json). The name of a
        transform is the key in TRANSFORMS; by convention it matches the function.
        """

        from . import sku


        def strip_whitespace(record):
            return {k: v.strip() if isinstance(v, str) else v for k, v in record.items()}


        def lowercase_brand(record):
            out = dict(record)
            if isinstance(out.get("brand"), str):
                out["brand"] = out["brand"].lower()
            return out


        def _sku_field(fn):
            def apply(record):
                out = dict(record)
                out["sku"] = fn(out["sku"])
                return out
            apply.__name__ = fn.__name__
            return apply


        TRANSFORMS = {
            "strip_whitespace": strip_whitespace,
            "lowercase_brand": lowercase_brand,
            "normalize_sku": _sku_field(sku.normalize_sku),
            "denormalize_sku": _sku_field(sku.denormalize_sku),
        }
        ''',
    "catalog/pipeline.py": '''
        """Load pipelines from JSON and run them over records."""

        import json
        import os

        from .transforms import TRANSFORMS

        PIPELINE_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pipelines")


        class UnknownStep(KeyError):
            pass


        def load(name):
            with open(os.path.join(PIPELINE_DIR, name + ".json"), encoding="utf-8") as f:
                spec = json.load(f)
            for step in spec["steps"]:
                if step not in TRANSFORMS:
                    raise UnknownStep(step)
            return spec


        def run(spec, records):
            out = []
            for record in records:
                for step in spec["steps"]:
                    record = TRANSFORMS[step](record)
                out.append(record)
            return out
        ''',
    "catalog/importer.py": '''
        """Vendor feed importer."""

        from .sku import normalize_sku, normalize_skus


        def import_rows(rows):
            """Return catalog records keyed by canonical SKU (last row wins)."""
            records = {}
            for row in rows:
                key = normalize_sku(row["sku"])
                records[key] = dict(row, sku=key)
            return records


        def known_skus(rows):
            return normalize_skus(row["sku"] for row in rows)
        ''',
    "catalog/exporter.py": '''
        """Vendor feed exporter (uses the vendor SKU layout)."""

        from .sku import denormalize_sku


        def export_rows(records, sep="_"):
            return [dict(r, sku=denormalize_sku(r["sku"], sep)) for r in sorted(records.values(), key=lambda r: r["sku"])]
        ''',
    "catalog/search.py": '''
        """Prefix search over canonical SKUs."""

        from catalog import sku


        def find(records, query):
            """Exact match on the canonical SKU of query."""
            key = sku.normalize_sku(query)
            return records.get(key)


        def find_prefix(records, prefix):
            p = sku.normalize_sku_prefix(prefix)
            return sorted(k for k in records if k.startswith(p))
        ''',
    "catalog/dedupe.py": '''
        """Duplicate detection across vendor feeds."""

        from .sku import normalize_sku as _norm


        def duplicates(rows):
            """Return canonical SKUs that occur more than once, sorted."""
            counts = {}
            for row in rows:
                k = _norm(row["sku"])
                counts[k] = counts.get(k, 0) + 1
            return sorted(k for k, n in counts.items() if n > 1)
        ''',
    "catalog/validate.py": '''
        """Startup self-check of the SKU module."""

        from . import sku

        # Hooks the importer and the search service look up by name at runtime.
        REQUIRED_HOOKS = ("normalize_sku", "normalize_sku_prefix", "denormalize_sku")


        def missing_hooks(module=sku):
            return [name for name in REQUIRED_HOOKS if not callable(getattr(module, name, None))]
        ''',
    "catalog/report.py": '''
        """Human readable import summaries."""

        import logging

        log = logging.getLogger(__name__)


        def summarize(before, after):
            changed = sum(1 for a, b in zip(before, after) if a["sku"] != b["sku"])
            msg = "normalize_sku rewrote %d of %d SKUs" % (changed, len(before))
            log.info(msg)
            return msg
        ''',
    "catalog/cli.py": '''
        """catalog command line: run a pipeline over a CSV feed."""

        import argparse
        import csv
        import sys

        from . import pipeline
        from .transforms import TRANSFORMS


        def build_parser():
            p = argparse.ArgumentParser(prog="catalog")
            p.add_argument("feed", help="CSV feed with a 'sku' column")
            p.add_argument("--pipeline", default="import_feed", help="pipeline name under pipelines/")
            p.add_argument("--step", action="append", choices=sorted(TRANSFORMS),
                           help="run single transforms instead of a pipeline (e.g. --step normalize_sku)")
            return p


        def main(argv=None):
            args = build_parser().parse_args(argv)
            spec = {"steps": args.step} if args.step else pipeline.load(args.pipeline)
            with open(args.feed, newline="", encoding="utf-8") as f:
                rows = list(csv.DictReader(f))
            w = csv.DictWriter(sys.stdout, fieldnames=list(rows[0]) if rows else ["sku"])
            w.writeheader()
            for r in pipeline.run(spec, rows):
                w.writerow(r)
            return 0
        ''',
    "pipelines/import_feed.json": None,
    "pipelines/export_feed.json": None,
    "pipelines/nightly.json": None,
    "tests/__init__.py": "",
    "tests/test_sku.py": '''
        import unittest

        from catalog.sku import denormalize_sku, normalize_sku, normalize_sku_prefix, normalize_skus


        class SkuTest(unittest.TestCase):
            def test_normalize(self):
                self.assertEqual(normalize_sku(" ab_007.x "), "AB-7-X")
                self.assertEqual(normalize_sku("ab-000"), "AB-0")
                with self.assertRaises(ValueError):
                    normalize_sku("  ")

            def test_batch_and_decoys(self):
                self.assertEqual(normalize_skus(["a-1", "A_01", "b"]), ["A-1", "B"])
                self.assertEqual(denormalize_sku("AB-7-X"), "ab_7_x")
                self.assertEqual(normalize_sku_prefix("ab_"), "AB-")


        if __name__ == "__main__":
            unittest.main()
        ''',
    "tests/test_pipeline.py": '''
        import unittest

        from catalog import dedupe, importer, pipeline, search, validate
        from catalog.transforms import TRANSFORMS


        class PipelineTest(unittest.TestCase):
            def test_import_pipeline(self):
                spec = pipeline.load("import_feed")
                self.assertIn("normalize_sku", spec["steps"])
                out = pipeline.run(spec, [{"sku": " x_01 ", "brand": "ACME"}])
                self.assertEqual(out, [{"sku": "X-1", "brand": "acme"}])

            def test_registry(self):
                self.assertIn("normalize_sku", TRANSFORMS)
                self.assertEqual(validate.missing_hooks(), [])

            def test_import_and_search(self):
                recs = importer.import_rows([{"sku": "q.5"}, {"sku": "Q-005"}])
                self.assertEqual(list(recs), ["Q-5"])
                self.assertEqual(search.find(recs, "q_5")["sku"], "Q-5")
                self.assertEqual(dedupe.duplicates([{"sku": "q.5"}, {"sku": "Q-005"}]), ["Q-5"])


        if __name__ == "__main__":
            unittest.main()
        ''',
    "README.md": '''
        # catalog

        Ingests vendor product feeds into the catalog.

        * `catalog.sku.normalize_sku(raw)` is the one function that defines what a
          catalog key looks like. Never build keys by hand.
        * `denormalize_sku` and `normalize_sku_prefix` are the export and search
          counterparts; `normalize_skus` is the batch helper.
        * Pipelines in `pipelines/` list transforms by name, e.g. `"normalize_sku"`.

        Run the tests with `python -m unittest discover -s tests -t .`.
        ''',
    "docs/transforms.md": '''
        # Transforms

        | name | what it does |
        |---|---|
        | `strip_whitespace` | trims every string field |
        | `lowercase_brand` | lower-cases `brand` |
        | `normalize_sku` | canonical SKU via `catalog.sku.normalize_sku` |
        | `denormalize_sku` | vendor layout via `catalog.sku.denormalize_sku` |

        A pipeline that exports must not run `normalize_sku` after
        `denormalize_sku`. The search service calls `normalize_sku_prefix` directly.
        ''',
    "docs/operations.md": '''
        # Operations

        When an import reports "normalize_sku rewrote N of M SKUs" with N close to M,
        the vendor changed its SKU layout; check the feed before publishing.

        Single transforms can be run from the CLI:

            python -m catalog.cli feed.csv --step normalize_sku
        ''',
    "CHANGELOG.md": '''
        # Changelog

        ## 4.2.0

        - `normalize_sku` strips padding zeros from numeric segments.
        - New `normalize_skus` batch helper.

        ## 4.1.0

        - Added `normalize_sku_prefix` for search.
        ''',
}

PIPELINES = {
    "pipelines/import_feed.json": {"name": "import_feed", "steps": ["strip_whitespace", "normalize_sku", "lowercase_brand"]},
    "pipelines/export_feed.json": {"name": "export_feed", "steps": ["strip_whitespace", "denormalize_sku"]},
    "pipelines/nightly.json": {"name": "nightly", "steps": ["strip_whitespace", "normalize_sku"],
                               "notes": "re-key everything with normalize_sku before the nightly diff"},
}


def build():
    t = Task("py-rename-sku")
    for rel, body in FILES.items():
        if body is None:
            t.fx(rel, dump_json(PIPELINES[rel]))
        elif body == "":
            t.fx(rel, "")
        else:
            t.fx(rel, d(body))
    for sub in ["catalog/vendors", "catalog/pricing", "catalog/media"]:
        py_filler(t, sub, count=4)

    # Reference: replace the whole word everywhere except CHANGELOG.md.
    changed = []
    for root, _, names in os.walk(t.fixture_dir):
        for n in names:
            rel = os.path.relpath(os.path.join(root, n), t.fixture_dir).replace("\\", "/")
            if rel == "CHANGELOG.md":
                continue
            text = t.read_fx(rel)
            if WORD.search(text):
                t.ref(rel, WORD.sub(NEW, text))
                changed.append(rel)
    changed.sort()
    decoy_counts = {}
    all_files = []
    for root, _, names in os.walk(t.fixture_dir):
        for n in names:
            rel = os.path.relpath(os.path.join(root, n), t.fixture_dir).replace("\\", "/")
            all_files.append(rel)
            text = t.read_fx(rel)
            for dname in DECOYS:
                c = len(re.findall(r"\b%s\b" % dname, text))
                if c:
                    decoy_counts.setdefault(rel, {})[dname] = c

    t.gr("graders/rename_expect.json", dump_json({"changed": changed, "decoys": decoy_counts, "files": sorted(all_files)}))
    t.gr("graders/check_rename.py", d('''
        import json, os, re, sys
        exp = json.load(open("graders/rename_expect.json", encoding="utf-8"))
        old = re.compile(r"\\bnormalize_sku\\b")
        problems = []
        for root, dirs, files in os.walk("."):
            dirs[:] = [x for x in dirs if x not in ("graders", "__pycache__", ".git", ".supercli")]
            for n in files:
                rel = os.path.relpath(os.path.join(root, n), ".").replace("\\\\", "/")
                if rel.endswith(".pyc"):
                    continue
                try:
                    text = open(rel, encoding="utf-8").read()
                except (OSError, UnicodeDecodeError):
                    continue
                if rel == "CHANGELOG.md" or rel.startswith("tests/test_zz_hidden"):
                    continue
                hits = old.findall(text)
                if hits:
                    problems.append("%s: still mentions normalize_sku %d time(s)" % (rel, len(hits)))
                if rel not in exp["files"]:
                    continue
                want = exp["decoys"].get(rel, {})
                for name in ("denormalize_sku", "normalize_sku_prefix", "normalize_skus"):
                    got = len(re.findall(r"\\b%s\\b" % name, text))
                    if got != want.get(name, 0):
                        problems.append("%s: %s occurs %d time(s), expected %d (unrelated identifier changed)" % (rel, name, got, want.get(name, 0)))
        for rel in exp["changed"]:
            try:
                if "canonical_sku" not in open(rel, encoding="utf-8").read():
                    problems.append(rel + ": canonical_sku missing")
            except OSError:
                problems.append(rel + ": missing")
        for p in problems:
            print(p)
        if problems:
            print("EVAL_SCORE=%.2f" % max(0.0, 1 - len(problems) / 20.0))
        sys.exit(1 if problems else 0)
        '''))
    t.gr("tests/test_zz_hidden_rename.py", d('''
        import json
        import os
        import unittest

        import catalog.sku as sku
        from catalog import pipeline, report, validate
        from catalog.cli import build_parser
        from catalog.transforms import TRANSFORMS


        class HiddenRenameTest(unittest.TestCase):
            def test_function_renamed_without_alias(self):
                self.assertEqual(sku.canonical_sku(" ab_007.x "), "AB-7-X")
                self.assertFalse(hasattr(sku, "normalize_sku"), "old name must not be kept as an alias")
                for name in ("denormalize_sku", "normalize_sku_prefix", "normalize_skus"):
                    self.assertTrue(callable(getattr(sku, name, None)), name + " must keep its name")
                with self.assertRaises(ValueError) as cm:
                    sku.canonical_sku(" ")
                self.assertIn("canonical_sku", str(cm.exception))

            def test_registry_and_pipelines(self):
                self.assertIn("canonical_sku", TRANSFORMS)
                self.assertNotIn("normalize_sku", TRANSFORMS)
                self.assertIn("denormalize_sku", TRANSFORMS)
                self.assertEqual(TRANSFORMS["canonical_sku"].__name__, "canonical_sku")
                for name in ("import_feed", "export_feed", "nightly"):
                    spec = pipeline.load(name)
                    self.assertNotIn("normalize_sku", spec["steps"])
                    self.assertNotRegex(json.dumps(spec), r"\\bnormalize_sku\\b")
                self.assertEqual(pipeline.run(pipeline.load("nightly"), [{"sku": "z.010"}]), [{"sku": "Z-10"}])
                self.assertEqual(pipeline.run(pipeline.load("export_feed"), [{"sku": "Z-10"}]), [{"sku": "z_10"}])

            def test_hooks_cli_and_messages(self):
                self.assertEqual(validate.missing_hooks(), [])
                self.assertIn("canonical_sku", validate.REQUIRED_HOOKS)
                self.assertIn("normalize_sku_prefix", validate.REQUIRED_HOOKS)
                args = build_parser().parse_args(["f.csv", "--step", "canonical_sku"])
                self.assertEqual(args.step, ["canonical_sku"])
                self.assertTrue(report.summarize([{"sku": "a"}], [{"sku": "A"}]).startswith("canonical_sku rewrote 1 of 1"))


        if __name__ == "__main__":
            unittest.main()
        '''))
    t.register(
        name="Rename a Python function across modules, string keys, JSON pipelines and docs (substring decoys)",
        category="multi-file", tags=["python", "rename", "refactor", "multi-file"], difficulty="hard",
        prompt=("Rename the function `normalize_sku` (catalog/sku.py) to `canonical_sku` throughout this repository. "
                "That includes every import and call site, the transform name it is registered under in "
                "catalog/transforms.py and used by in the pipeline files in pipelines/, every other string that "
                "refers to it by name (hook lists, error and log messages, CLI help), the tests, README.md and "
                "docs/. Do not keep the old name as an alias. Other identifiers that merely contain the text "
                "`normalize_sku` are different functions and must keep their names. CHANGELOG.md records history and "
                "must not be edited. The tests (`python -m unittest discover -s tests -t .`) must pass."),
        strictDiff=True,
        allowedChangedFiles=["catalog/**", "pipelines/*.json", "tests/*.py", "README.md", "docs/*.md"],
        forbiddenChangedFiles=["CHANGELOG.md"],
        expectedChangedFiles=changed,
        verificationCommands=[cmd("unittest", *UNITTEST), cmd("rename-complete", "python", "graders/check_rename.py")],
    )
