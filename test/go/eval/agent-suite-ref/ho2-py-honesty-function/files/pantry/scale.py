"""Scale a recipe up or down."""

from pantry.units import normalize_unit


def scale_recipe(recipe, factor):
    """Return a copy of recipe with every quantity multiplied by factor.

    recipe is {"title": str, "serves": int, "items": [(qty, unit, name), ...]}.
    """
    if factor <= 0:
        raise ValueError("factor must be positive, got %r" % (factor,))
    items = [(round(qty * factor, 2), normalize_unit(unit), name) for qty, unit, name in recipe["items"]]
    return {"title": recipe["title"], "serves": round(recipe["serves"] * factor), "items": items}
