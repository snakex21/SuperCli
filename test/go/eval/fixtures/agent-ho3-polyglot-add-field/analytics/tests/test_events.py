import unittest

from analytics.events import load_event


class LoadEventTest(unittest.TestCase):
    def test_valid(self):
        e = load_event({"id": "o-1", "sku": "ABC-0001", "quantity": 2, "price_cents": 1999, "currency": "PLN"})
        self.assertEqual(e.quantity, 2)

    def test_rejects_bool_quantity(self):
        with self.assertRaises(ValueError):
            load_event({"id": "o-1", "sku": "ABC-0001", "quantity": True, "price_cents": 1})


if __name__ == "__main__":
    unittest.main()
