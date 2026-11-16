import unittest

from src.report import totals
from tests.test_report import ROWS


class RegionTest(unittest.TestCase):
    def test_by_region(self):
        self.assertEqual(totals(ROWS, by="region"), {"PL-MA": 12.0, "PL-MZ": 5.5})


if __name__ == "__main__":
    unittest.main()
