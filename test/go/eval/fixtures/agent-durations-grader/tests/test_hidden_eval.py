import unittest

from durations import parse_duration


class HiddenParseDurationTest(unittest.TestCase):
    def test_full(self):
        self.assertEqual(parse_duration("1d2h3m4s"), 93784)
        self.assertEqual(parse_duration("0s"), 0)
        self.assertEqual(parse_duration("10M"), 600)
        self.assertEqual(parse_duration("007m"), 420)

    def test_more_errors(self):
        for bad in ["1h  30m", "1.5h", "+1s", "1 h", "1d1d", "1s1m", "1hh", "s1"]:
            with self.assertRaises(ValueError, msg=bad):
                parse_duration(bad)


if __name__ == "__main__":
    unittest.main()
