import unittest

from invoicer.models import Discount, Invoice, LineItem
from invoicer.pricing import discount_cents, total_cents


def inv(code=None, unit=10000):
    return Invoice("1", "c", [LineItem("a", 1, unit)], tax_rate_bp=2300, discount_code=code)


class DiscountTest(unittest.TestCase):
    def test_validation(self):
        with self.assertRaises(ValueError):
            Discount("X")
        with self.assertRaises(ValueError):
            Discount("X", percent=10, amount_cents=5)

    def test_percent(self):
        self.assertEqual(discount_cents(inv("p"), {"P": Discount("P", percent=10)}), 1000)

    def test_amount_capped(self):
        self.assertEqual(discount_cents(inv("a", unit=300), {"A": Discount("A", amount_cents=500)}), 300)

    def test_tax_after_discount(self):
        self.assertEqual(total_cents(inv("p"), {"P": Discount("P", percent=10)}), 11070)

    def test_unknown(self):
        with self.assertRaises(KeyError):
            total_cents(inv("nope"), {})
