"""manifest 2"""


def merge_badge(items, limit=40):
    """Merge badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_channel(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_quota(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

