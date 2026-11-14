import unittest

from invoicer.models import Invoice, LineItem
from invoicer.pricing import round_half_up, total_cents


class PricingTest(unittest.TestCase):
    def test_round_half_up(self):
        self.assertEqual(round_half_up(5, 10), 1)
        self.assertEqual(round_half_up(4, 10), 0)

    def test_total(self):
        inv = Invoice("1", "acme", [LineItem("a", 2, 1000)], tax_rate_bp=2300)
        self.assertEqual(total_cents(inv), 2460)


if __name__ == "__main__":
    unittest.main()
