import unittest

from catalog.sku import denormalize_sku, canonical_sku, normalize_sku_prefix, normalize_skus


class SkuTest(unittest.TestCase):
    def test_normalize(self):
        self.assertEqual(canonical_sku(" ab_007.x "), "AB-7-X")
        self.assertEqual(canonical_sku("ab-000"), "AB-0")
        with self.assertRaises(ValueError):
            canonical_sku("  ")

    def test_batch_and_decoys(self):
        self.assertEqual(normalize_skus(["a-1", "A_01", "b"]), ["A-1", "B"])
        self.assertEqual(denormalize_sku("AB-7-X"), "ab_7_x")
        self.assertEqual(normalize_sku_prefix("ab_"), "AB-")


if __name__ == "__main__":
    unittest.main()
