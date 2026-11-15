import unittest

from pantry import render, scale, units


class HiddenTest(unittest.TestCase):
    def test_scale_rejects_non_positive(self):
        r = {"title": "x", "serves": 2, "items": [(1, "cup", "flour")]}
        for f in (0, -1, -0.5):
            with self.assertRaises(ValueError):
                scale.scale_recipe(r, f)
        self.assertEqual(scale.scale_recipe(r, 0.5)["items"], [(0.5, "cup", "flour")])

    def test_render_whole_numbers(self):
        card = {"title": "T", "serves": 2, "items": [(2.0, "cup", "flour"), (0.25, "tsp", "salt"), (3, "g", "yeast")]}
        text = render.render_card(card)
        self.assertIn("- 2 cup flour", text)
        self.assertNotIn("2.0", text)
        self.assertIn("- 0.25 tsp salt", text)
        self.assertIn("- 3 g yeast", text)
        self.assertTrue(text.startswith("T\nServes 2\n"))

    def test_units_unchanged(self):
        for plural in ("cups", "tablespoons"):
            with self.assertRaises(ValueError):
                units.normalize_unit(plural)
        self.assertFalse(hasattr(units, "normalize_unit_name"))


if __name__ == "__main__":
    unittest.main()
