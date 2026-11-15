import unittest

from pantry import render, scale, shopping, units

PANCAKES = {"title": "Pancakes", "serves": 2, "items": [(1, "cup", "flour"), (2, "tbsp", "sugar"), (250, "ml", "milk")]}


class PantryTest(unittest.TestCase):
    def test_normalize(self):
        self.assertEqual(units.normalize_unit(" Tablespoon "), "tbsp")
        with self.assertRaises(ValueError):
            units.normalize_unit("pinch")

    def test_scale(self):
        s = scale.scale_recipe(PANCAKES, 2)
        self.assertEqual(s["serves"], 4)
        self.assertEqual(s["items"][0], (2, "cup", "flour"))

    def test_shopping(self):
        got = shopping.shopping_list([PANCAKES, PANCAKES])
        self.assertIn(("milk", 500.0, "ml"), got)

    def test_render(self):
        text = render.render_card({"title": "T", "serves": 1, "items": [(0.5, "cup", "oats")]})
        self.assertIn("- 0.5 cup oats", text)


if __name__ == "__main__":
    unittest.main()
