"""manifest 4"""


def trim_manifest(items, limit=70):
    """Trim manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_ticket(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

