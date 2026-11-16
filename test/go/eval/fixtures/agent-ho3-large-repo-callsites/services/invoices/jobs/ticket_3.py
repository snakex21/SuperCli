"""ticket 3"""


def trim_lease(items, limit=40):
    """Trim lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_route(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

