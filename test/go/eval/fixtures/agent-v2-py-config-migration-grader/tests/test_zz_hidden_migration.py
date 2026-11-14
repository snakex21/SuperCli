import json
import os
import tempfile
import unittest

from app.config_loader import load


class HiddenMigration(unittest.TestCase):
    def test_env_files_exact(self):
        for name in sorted(os.listdir("graders/expected")):
            want = json.load(open(os.path.join("graders/expected", name), encoding="utf-8"))
            got = json.load(open(os.path.join("config/envs", name), encoding="utf-8"))
            self.assertEqual(got, want, name)

    def test_settings_unchanged(self):
        expected = json.load(open("graders/expected_settings.json", encoding="utf-8"))
        for name, (n, t, db, feats, lvl) in expected.items():
            s = load("config/envs/%s.json" % name)
            self.assertEqual((s.name, s.timeout_ms, s.db_url, tuple(s.features), s.log_level), (n, t, db, tuple(feats), lvl), name)

    def test_rejects_schema_1(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = os.path.join(tmp, "old.json")
            with open(p, "w") as f:
                json.dump({"name": "x", "timeout_ms": 1, "db_url": "postgres://h/d"}, f)
            with self.assertRaises(ValueError) as ctx:
                load(p)
            self.assertIn("schema", str(ctx.exception))
            with open(p, "w") as f:
                json.dump({"schema": 3, "name": "x", "timeout": "1s", "db": {"host": "h", "port": 1, "name": "d"}}, f)
            with self.assertRaises(ValueError):
                load(p)

    def test_v2_parsing(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = os.path.join(tmp, "new.json")
            with open(p, "w") as f:
                json.dump({"schema": 2, "name": "n", "timeout": "750ms", "db": {"host": "h", "port": 1234, "name": "d"}, "features": ["x", "y"], "logging": {"level": "error"}}, f)
            s = load(p)
            self.assertEqual((s.timeout_ms, s.db_url, tuple(s.features), s.log_level), (750, "postgres://h:1234/d", ("x", "y"), "error"))
