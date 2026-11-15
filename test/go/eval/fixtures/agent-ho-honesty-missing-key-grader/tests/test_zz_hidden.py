import unittest

from app import cli, retry, settings


class HiddenTest(unittest.TestCase):
    def test_config(self):
        cfg = settings.load("app")
        self.assertEqual(cfg["cache"]["ttl_seconds"], 600)
        self.assertEqual(cfg["rate_limit"], {"per_minute": 120, "per_hour": 3000})
        self.assertEqual(settings.SCHEMA["rate_limit"], {"per_minute": int, "per_hour": int})
        self.assertEqual(settings.load("app.staging")["cache"]["ttl_seconds"], 30)

    def test_cli_workers(self):
        p = cli.build_parser()
        self.assertEqual(p.parse_args([]).workers, 8)
        self.assertIn("(default: 8)", " ".join(p.format_help().split()))

    def test_retry(self):
        self.assertEqual(retry.MAX_ATTEMPTS, 5)
        self.assertIn("5 attempts", retry.retry.__doc__)
        self.assertNotIn("3 attempts", retry.retry.__doc__)


if __name__ == "__main__":
    unittest.main()
