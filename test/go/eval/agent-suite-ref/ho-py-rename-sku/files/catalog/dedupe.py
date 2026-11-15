"""Duplicate detection across vendor feeds."""

from .sku import canonical_sku as _norm


def duplicates(rows):
    """Return canonical SKUs that occur more than once, sorted."""
    counts = {}
    for row in rows:
        k = _norm(row["sku"])
        counts[k] = counts.get(k, 0) + 1
    return sorted(k for k, n in counts.items() if n > 1)
