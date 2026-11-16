"""badge 3"""


def build_ledger(items, limit=50):
    """Build ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_lease(items, limit=60):
    """Build lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_quota(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

