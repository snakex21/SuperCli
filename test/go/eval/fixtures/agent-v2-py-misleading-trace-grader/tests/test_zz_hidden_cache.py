import json
import os
import tempfile
import unittest

from cache import reader, writer


class HiddenCache(unittest.TestCase):
    def roundtrip(self, obj):
        with tempfile.TemporaryDirectory() as tmp:
            p = os.path.join(tmp, "e.json")
            writer.write(p, obj)
            text = open(p, encoding="utf-8").read()
            return json.loads(text)

    def test_writer_strict_json(self):
        self.assertEqual(self.roundtrip({"a": None, "b": 1, "c": None}), {"b": 1})
        self.assertEqual(self.roundtrip({"a": None}), {})
        self.assertEqual(self.roundtrip({}), {})
        self.assertEqual(self.roundtrip({'say "hi"': "x\\y", "n": [1, None]}), {'say "hi"': "x\\y", "n": [1, None]})

    def test_reader_stays_strict(self):
        for bad in ('{"a": 1,}', '{"a": 1, , "b": 2}', "{'a': 1}", "{"):
            with tempfile.TemporaryDirectory() as tmp:
                p = os.path.join(tmp, "bad.json")
                with open(p, "w", encoding="utf-8") as f:
                    f.write(bad)
                with self.assertRaises(ValueError, msg=bad):
                    reader.load(p)
