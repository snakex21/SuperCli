"""Merge the ingredients of several recipes into one shopping list."""

from pantry.units import convert, normalize_unit, VOLUME


def shopping_list(recipes):
    """Sum quantities per ingredient; volumes are summed in ml."""
    totals = {}
    for recipe in recipes:
        for qty, unit, name in recipe["items"]:
            unit = normalize_unit(unit)
            if unit in VOLUME:
                qty, unit = convert(qty, unit, "ml"), "ml"
            key = (name, unit)
            totals[key] = totals.get(key, 0) + qty
    return sorted((name, round(qty, 2), unit) for (name, unit), qty in totals.items())
