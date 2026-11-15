import datetime
import unittest

from blogkit import slugify, slugify_filename
from blogkit.attachments import attachment_path
from blogkit.posts import Post


class TextTest(unittest.TestCase):
    def test_slugify(self):
        self.assertEqual(slugify("Hello, World!"), "hello-world")
        self.assertEqual(slugify("Crème brûlée"), "creme-brulee")
        self.assertEqual(slugify("a" * 80), "a" * 60)

    def test_filename(self):
        self.assertEqual(slugify_filename("My Photo (1).JPG"), "My_Photo_1.jpg")
        self.assertEqual(attachment_path("p", "x y.png"), "media/p/x_y.png")

    def test_permalinks(self):
        old = Post("Hello  World", datetime.date(2023, 5, 1))
        new = Post("Hello  World", datetime.date(2024, 5, 1))
        self.assertEqual(old.permalink(), "/2023/05/hello-world/")
        self.assertEqual(new.permalink(), "/2024/05/hello-world/")


if __name__ == "__main__":
    unittest.main()
