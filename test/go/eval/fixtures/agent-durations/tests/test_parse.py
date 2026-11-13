import unittest

from durations import parse_duration


class ParseDurationTest(unittest.TestCase):
    def test_simple(self):
        self.assertEqual(parse_duration("45s"), 45)
        self.assertEqual(parse_duration("1h30m"), 5400)
        self.assertEqual(parse_duration("2d 4h"), 187200)

    def test_case_and_whitespace(self):
        self.assertEqual(parse_duration("  1H 1M "), 3660)

    def test_errors(self):
        for bad in ["", "   ", "5", "h", "1x", "1m1h", "1h1h", "-1s"]:
            with self.assertRaises(ValueError, msg=bad):
                parse_duration(bad)


if __name__ == "__main__":
    unittest.main()
