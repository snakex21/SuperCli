import os
import unittest

from tools.import_klienci import load_customers

HERE = os.path.dirname(os.path.abspath(__file__))


class ImportTest(unittest.TestCase):
    def test_loads_all_customers(self):
        rows = load_customers(os.path.join(HERE, "..", "data", "klienci.csv"))
        self.assertEqual([r["id"] for r in rows], [1, 2, 3])
        self.assertEqual(rows[0]["city"], "Łódź")
        self.assertEqual(rows[2]["name"], "Gęś; Kaczka i Wspólnicy")


if __name__ == "__main__":
    unittest.main()
