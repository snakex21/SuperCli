"""token 3"""


def score_window(items, limit=70):
    """Score window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_cursor(items, limit=60):
    """Split cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_batch(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

