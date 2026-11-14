import os
import unittest

from app.config_loader import Settings, load

HERE = os.path.dirname(__file__)


class LoaderTest(unittest.TestCase):
    def test_sample(self):
        s = load(os.path.join(HERE, "fixtures", "sample.json"))
        self.assertEqual(s, Settings("svc", 2500, "postgres://h:7000/d", ("a", "b"), "warning"))

    def test_all_envs_load(self):
        for name in os.listdir("config/envs"):
            load(os.path.join("config/envs", name))
