import unittest
from datetime import date
from decimal import Decimal

from odsetki.kalkulator import do_zaplaty, dni_opoznienia, nalicz_odsetki


class KalkulatorTest(unittest.TestCase):
    def test_dni_opoznienia(self):
        self.assertEqual(dni_opoznienia(date(2025, 1, 31), date(2025, 3, 2)), 30)
        self.assertEqual(dni_opoznienia(date(2025, 1, 31), date(2025, 1, 20)), 0)

    def test_zaplata_w_terminie(self):
        self.assertEqual(nalicz_odsetki("1000.00", "0.1125", date(2025, 5, 5), date(2025, 5, 5)), Decimal("0.00"))

    def test_nalicz_odsetki_rok(self):
        # 10 000 zł, 11,25% rocznie, 30 dni opóźnienia, rok 365-dniowy
        self.assertEqual(nalicz_odsetki("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)), Decimal("92.47"))


if __name__ == "__main__":
    unittest.main()
