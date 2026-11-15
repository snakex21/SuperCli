import datetime
import unittest

import blogkit
from blogkit import text
from blogkit.compat import v1
from blogkit.posts import Post

KEEP = [[["slugify_filename", ["Łódź nocą.JPG"]], "odz_noca.jpg"], [["slugify_filename", ["Żółć.txt"]], "Zoc.txt"], [["slugify_filename", ["ŁŁ"]], "file"], [["_slugify_legacy", ["Łódź nocą"]], "odz-noca"], [["_slugify_legacy", ["Zażółć Gęślą Jaźń"]], "zazoc-gesla-jazn"]]


class HiddenTest(unittest.TestCase):
    def test_slugify_polish(self):
        cases = {
            "Łódź nocą": "lodz-noca",
            "Żółta łąka": "zolta-laka",
            "ZAŻÓŁĆ GĘŚLĄ JAŹŃ": "zazolc-gesla-jazn",
            "Hello, World!": "hello-world",
            "Crème brûlée": "creme-brulee",
            "  --Ćma i ŁOŚ--  ": "cma-i-los",
        }
        for title, want in cases.items():
            self.assertEqual(blogkit.slugify(title), want, title)
        self.assertEqual(blogkit.slugify("ł" * 80), "l" * 60)
        self.assertEqual(blogkit.slugify("Łódź", max_len=3), "lod")

    def test_new_permalink(self):
        self.assertEqual(Post("Łódź nocą", datetime.date(2025, 2, 1)).permalink(), "/2025/02/lodz-noca/")

    def test_siblings_unchanged(self):
        for (fn, args), want in KEEP:
            self.assertEqual(getattr(text, fn)(*args), want, (fn, args))
        self.assertEqual(Post("Łódź nocą", datetime.date(2020, 2, 1)).slug, 'odz-noca')
        self.assertEqual(v1.slugify("Łódź nocą"), 'odz-noca')


if __name__ == "__main__":
    unittest.main()
