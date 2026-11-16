"""window 3"""


def merge_quota(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_slot(items, limit=20):
    """Plan slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_folio(items, limit=50):
    """Score folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_ticket(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

