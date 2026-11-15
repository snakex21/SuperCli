import os
import unittest

from sheetcalc import Sheet, evaluate, parse_ref, report
from sheetcalc.engine import col_to_index, index_to_col

DATA = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "june.csv")


class HiddenTest(unittest.TestCase):
    def test_columns(self):
        cases = {"A": 0, "Z": 25, "AA": 26, "AB": 27, "AZ": 51, "BA": 52, "ZZ": 701, "AAA": 702, "abc": 730}
        for letters, idx in cases.items():
            self.assertEqual(col_to_index(letters), idx, letters)
        for i in range(0, 2000, 7):
            self.assertEqual(col_to_index(index_to_col(i)), i)
        self.assertEqual(parse_ref("AE2"), (1, 30))
        self.assertEqual(parse_ref("$AA$10"), (9, 26))

    def test_cells_past_z(self):
        s = Sheet()
        s.set("A1", 1)
        s.set("AA1", 2)
        s.set("Z1", 4)
        s.set("AB1", 8)
        self.assertEqual(s.get("A1"), 1)
        self.assertEqual(evaluate(s, "=SUM(Z1:AB1)"), 14)
        self.assertEqual(evaluate(s, "=AA1*10"), 20)

    def test_report(self):
        stations, total = report.summarize(DATA)
        self.assertEqual(stations, {'Rynek': (7260, 242.0, 28), 'Dworzec': (6057, 201.9, 30), 'Kampus': (6343, 211.43, 16), 'Port': (6242, 208.07, 29), 'Arena': (6237, 207.9, 28), 'Ogrod': (6856, 228.53, 29), 'Stocznia': (6924, 230.8, 26), 'Lotnisko': (5442, 181.4, 29), 'Zamek': (7177, 239.23, 26), 'Most': (7110, 237.0, 28), 'Park': (6661, 222.03, 8), 'Targ': (6006, 200.2, 12)})
        self.assertEqual(total, 78315)


if __name__ == "__main__":
    unittest.main()
