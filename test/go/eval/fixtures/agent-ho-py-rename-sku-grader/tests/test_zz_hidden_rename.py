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
            self.assertNotRegex(json.dumps(spec), r"\bnormalize_sku\b")
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
