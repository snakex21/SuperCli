import unittest

from inventory.api import to_dict
from inventory.reports import low_stock, summary
from inventory.stock import OutOfStock, Stock


class StockTest(unittest.TestCase):
    def setUp(self):
        self.stock = Stock()
        self.stock.add("A-100", 10)
        self.stock.add("B-200", 3)

    def test_add_accumulates(self):
        self.stock.add("A-100", 5)
        self.assertEqual(self.stock.get("A-100").qty_on_hand, 15)

    def test_reserve_and_ship(self):
        self.assertEqual(self.stock.reserve("A-100", 4), 6)
        self.stock.ship("A-100", 4)
        item = self.stock.get("A-100")
        self.assertEqual((item.qty_on_hand, item.reserved), (6, 0))

    def test_reserve_too_much(self):
        with self.assertRaises(OutOfStock):
            self.stock.reserve("B-200", 4)

    def test_reports(self):
        self.assertEqual(low_stock(self.stock), ["B-200"])
        self.assertIn("B-200", summary(self.stock))

    def test_api_view(self):
        self.assertEqual(to_dict(self.stock.get("B-200")),
                         {"sku": "B-200", "qty_on_hand": 3, "reserved": 0, "available": 3})


if __name__ == "__main__":
    unittest.main()
