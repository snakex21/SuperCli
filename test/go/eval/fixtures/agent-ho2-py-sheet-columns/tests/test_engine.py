import unittest

from sheetcalc import FormulaError, Sheet, cell_name, evaluate, parse_ref


class EngineTest(unittest.TestCase):
    def test_refs(self):
        self.assertEqual(parse_ref("B12"), (11, 1))
        self.assertEqual(parse_ref("$C$3"), (2, 2))
        self.assertEqual(cell_name(0, 0), "A1")
        self.assertEqual(cell_name(9, 25), "Z10")

    def test_arithmetic(self):
        s = Sheet()
        self.assertEqual(evaluate(s, "=1+2*3"), 7)
        self.assertEqual(evaluate(s, "=2^3^2"), 512)
        self.assertEqual(evaluate(s, "=-2^2"), 4)
        self.assertEqual(evaluate(s, "=50%"), 0.5)
        self.assertEqual(evaluate(s, '="a"&1'), "a1")

    def test_cells_and_ranges(self):
        s = Sheet()
        for i, v in enumerate([3, 1, 4, 1, 5]):
            s.set("B%d" % (i + 1), v)
        s.set("C1", "=SUM(B1:B5)")
        s.set("C2", "=MATCH(MAX(B1:B5), B1:B5, 0)")
        s.set("C3", "=IF(C1>10, \"big\", \"small\")")
        self.assertEqual(s.get("C1"), 14)
        self.assertEqual(s.get("C2"), 5)
        self.assertEqual(s.get("C3"), "big")
        s.set("B5", 0)
        self.assertEqual(s.get("C1"), 9)

    def test_functions(self):
        s = Sheet()
        self.assertEqual(evaluate(s, "=ROUND(2.375, 2)"), 2.38)
        self.assertEqual(evaluate(s, "=MEDIAN(1, 9, 3)"), 3)
        self.assertAlmostEqual(evaluate(s, "=KM2MI(1.609344)"), 1.0)
        self.assertEqual(evaluate(s, '=TRIM("  a   b ")'), "a b")

    def test_errors(self):
        s = Sheet()
        with self.assertRaises(FormulaError):
            evaluate(s, "=1/0")
        with self.assertRaises(FormulaError):
            evaluate(s, "=NOPE(1)")
        s.set("A1", "=A2")
        s.set("A2", "=A1")
        with self.assertRaises(Exception):
            s.get("A1")


if __name__ == "__main__":
    unittest.main()
