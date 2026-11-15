import os
import unittest

from sheetcalc import report

DATA = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "june.csv")


class ReportTest(unittest.TestCase):
    def test_june(self):
        stations, total = report.summarize(DATA)
        self.assertEqual(len(stations), 12)
        want = {'Rynek': (7260, 242.0, 28), 'Ogrod': (6856, 228.53, 29), 'Most': (7110, 237.0, 28)}
        for name, expected in want.items():
            self.assertEqual(stations[name], expected, name)
        self.assertEqual(total, 78315)

    def test_render(self):
        text = report.render(DATA)
        self.assertTrue(text.startswith("station"))
        self.assertIn("ALL", text)


if __name__ == "__main__":
    unittest.main()
