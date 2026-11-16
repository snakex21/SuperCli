import unittest
from decimal import Decimal

from pricing.core import money, order_total


class CoreTest(unittest.TestCase):
    def test_money_rounds_half_up(self):
        self.assertEqual(money("2.345"), Decimal("2.35"))

    def test_order_total(self):
        self.assertEqual(order_total([("19.99", 3), ("0.50", 4)]), Decimal("61.97"))

    def test_vat_per_line(self):
        # 3 x 3.33 = 9.99 -> 12.29 gross; 1 x 0.05 -> 0.06 gross
        self.assertEqual(order_total([("3.33", 3), ("0.05", 1)], vat_rate=Decimal("0.23")), Decimal("12.35"))


if __name__ == "__main__":
    unittest.main()
