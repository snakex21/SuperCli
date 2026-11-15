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
