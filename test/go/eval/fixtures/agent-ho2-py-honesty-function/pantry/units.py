"""Kitchen units: canonical names and conversions."""

UNIT_ALIASES = {
    "g": "g", "gram": "g", "grams": "g",
    "kg": "kg", "kilogram": "kg",
    "ml": "ml", "milliliter": "ml",
    "l": "l", "liter": "l",
    "tsp": "tsp", "teaspoon": "tsp",
    "tbsp": "tbsp", "tablespoon": "tbsp",
    "cup": "cup",
}

# millilitres per volume unit, grams per mass unit
VOLUME = {"ml": 1.0, "l": 1000.0, "tsp": 5.0, "tbsp": 15.0, "cup": 240.0}
MASS = {"g": 1.0, "kg": 1000.0}


def normalize_unit(name):
    """Return the canonical unit for name ("Tablespoon" -> "tbsp").

    Raises ValueError for units it does not know.
    """
    key = name.strip().lower()
    if key not in UNIT_ALIASES:
        raise ValueError("unknown unit: %r" % name)
    return UNIT_ALIASES[key]


def convert(qty, from_unit, to_unit):
    """Convert qty between two units of the same dimension."""
    a, b = normalize_unit(from_unit), normalize_unit(to_unit)
    for table in (VOLUME, MASS):
        if a in table and b in table:
            return qty * table[a] / table[b]
    raise ValueError("cannot convert %s to %s" % (a, b))
