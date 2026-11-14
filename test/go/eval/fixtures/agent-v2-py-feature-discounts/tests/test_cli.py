import io
import json
import os
import tempfile
import unittest

from invoicer.cli import main


class CliTest(unittest.TestCase):
    def test_total(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "inv.json")
            with open(path, "w") as f:
                json.dump({"number": "7", "customer": "x", "tax_rate_bp": 1000,
                           "items": [{"sku": "a", "qty": 1, "unit_cents": 1999}]}, f)
            out = io.StringIO()
            main(["total", path], out=out)
            self.assertEqual(out.getvalue().splitlines(), ["Subtotal: 19.99", "Tax: 2.00", "Total: 21.99"])
