import unittest

from textutils.utils import top_words, word_count


class UtilsTest(unittest.TestCase):
    def test_word_count_spaces(self):
        self.assertEqual(word_count("a b  c"), 3)

    def test_word_count_any_whitespace(self):
        self.assertEqual(word_count("a\tb\nc  d"), 4)

    def test_top_words(self):
        self.assertEqual(top_words("b a b c b a"), ["b", "a", "c"])


if __name__ == "__main__":
    unittest.main()
