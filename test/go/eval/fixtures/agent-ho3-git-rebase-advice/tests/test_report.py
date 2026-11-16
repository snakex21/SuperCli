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
