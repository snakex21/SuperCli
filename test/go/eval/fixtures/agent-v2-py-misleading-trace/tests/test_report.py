import tempfile
import unittest

from app.report import build


class ReportTest(unittest.TestCase):
    def test_full_stats(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = build({"user": "ann", "requests": 3, "last_error": "timeout", "plan": "pro"}, tmp)
            self.assertEqual(got, {"user": "ann", "requests": 3, "last_error": "timeout", "plan": "pro"})

    def test_without_plan(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = build({"user": "bob", "requests": 1, "last_error": "x"}, tmp)
            self.assertEqual(got, {"user": "bob", "requests": 1, "last_error": "x"})

    def test_clean_user(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = build({"user": "cid", "requests": 0, "plan": "free"}, tmp)
            self.assertEqual(got, {"user": "cid", "requests": 0, "plan": "free"})
