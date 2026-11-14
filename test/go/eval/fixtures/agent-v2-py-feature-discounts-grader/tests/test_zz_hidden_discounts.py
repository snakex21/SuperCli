import io
import json
import os
import tempfile
import unittest

from invoicer.cli import main
from invoicer.models import Discount, Invoice, LineItem
from invoicer.pricing import discount_cents, total_cents
from invoicer.storage import load_discounts, load_invoice, save_invoice


def make(code=None, units=(1999, 1), tax=2300):
    return Invoice("9", "c", [LineItem("s", units[1], units[0])], tax_rate_bp=tax, discount_code=code)


class HiddenDiscounts(unittest.TestCase):
    def test_model_validation(self):
        for kwargs in ({}, {"percent": 5, "amount_cents": 5}, {"percent": 0}, {"percent": 101}, {"amount_cents": 0}, {"amount_cents": -3}):
            with self.assertRaises(ValueError, msg=str(kwargs)):
                Discount("X", **kwargs)
        self.assertEqual(Discount("X", percent=100).min_subtotal_cents, 0)
        self.assertIsNone(Invoice("1", "c").discount_code)

    def test_percent_rounding_half_up(self):
        # 1999 * 15% = 299.85 -> 300 ; 1990 * 25% = 497.5 -> 498
        self.assertEqual(discount_cents(make("q15"), {"Q15": Discount("Q15", percent=15)}), 300)
        self.assertEqual(discount_cents(make("q25", (1990, 1)), {"q25": Discount("q25", percent=25)}), 498)

    def test_case_insensitive_and_unknown(self):
        d = {"Spring10": Discount("Spring10", percent=10)}
        self.assertEqual(discount_cents(make("SPRING10", (1000, 1)), d), 100)
        self.assertEqual(discount_cents(make("spring10", (1000, 1)), d), 100)
        with self.assertRaises(KeyError):
            discount_cents(make("other"), d)
        with self.assertRaises(KeyError):
            total_cents(make("x"))
        self.assertEqual(discount_cents(make(None), d), 0)

    def test_min_subtotal_and_cap(self):
        d = {"BIG": Discount("BIG", amount_cents=5000, min_subtotal_cents=2000), "CAP": Discount("CAP", amount_cents=999999)}
        self.assertEqual(discount_cents(make("big", (1999, 1)), d), 0)
        self.assertEqual(discount_cents(make("big", (1000, 2)), d), 2000)
        self.assertEqual(discount_cents(make("big", (3000, 2)), d), 5000)
        self.assertEqual(discount_cents(make("cap", (1500, 1)), d), 1500)
        self.assertEqual(total_cents(make("cap", (1500, 1)), d), 0)

    def test_tax_on_discounted_amount(self):
        d = {"P": Discount("P", percent=10)}
        # subtotal 1999, discount 200, taxable 1799, tax 23% = 413.77 -> 414
        self.assertEqual(total_cents(make("p"), d), 1799 + 414)
        self.assertEqual(total_cents(make(None)), 1999 + 460)

    def test_storage_roundtrip_and_legacy(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = os.path.join(tmp, "i.json")
            save_invoice(make(None), p)
            self.assertNotIn("discount_code", json.load(open(p)))
            save_invoice(make("P"), p)
            self.assertEqual(load_invoice(p).discount_code, "P")
            with open(p, "w") as f:
                json.dump({"number": "1", "customer": "c", "items": []}, f)
            self.assertIsNone(load_invoice(p).discount_code)
            dp = os.path.join(tmp, "d.json")
            with open(dp, "w") as f:
                json.dump([{"code": "A", "percent": 5}, {"code": "B", "amount_cents": 100, "min_subtotal_cents": 50}], f)
            ds = load_discounts(dp)
            self.assertEqual(set(ds), {"A", "B"})
            self.assertEqual(ds["B"].min_subtotal_cents, 50)
            self.assertIsInstance(ds["A"], Discount)

    def test_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            ip, dp = os.path.join(tmp, "i.json"), os.path.join(tmp, "d.json")
            save_invoice(make("spring", (1999, 1), 2300), ip)
            with open(dp, "w") as f:
                json.dump([{"code": "SPRING", "percent": 10}], f)
            out = io.StringIO()
            main(["total", ip, "--discounts", dp], out=out)
            self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Discount (SPRING): -2.00", "Tax: 4.14", "Total: 22.13"])
            save_invoice(make(None), ip)
            out = io.StringIO()
            main(["total", ip], out=out)
            self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Tax: 4.60", "Total: 24.59"])
