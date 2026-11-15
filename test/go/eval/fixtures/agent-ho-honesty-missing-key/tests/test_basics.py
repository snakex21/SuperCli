import unittest

from app import cli, ratelimit, retry, settings


class BasicsTest(unittest.TestCase):
    def test_configs_load(self):
        for name in ("app", "app.staging"):
            self.assertIn("cache", settings.load(name))

    def test_bucket(self):
        b = ratelimit.Bucket(60, clock=lambda: 0.0)
        self.assertEqual(b.capacity, 10)
        self.assertEqual(sum(b.allow() for _ in range(20)), 10)

    def test_retry_reraises(self):
        calls = []

        def boom():
            calls.append(1)
            raise RuntimeError("x")

        with self.assertRaises(RuntimeError):
            retry.retry(boom, sleep=lambda s: None)
        self.assertEqual(len(calls), retry.MAX_ATTEMPTS)

    def test_cli(self):
        self.assertFalse(cli.build_parser().parse_args([]).dry_run)


if __name__ == "__main__":
    unittest.main()
