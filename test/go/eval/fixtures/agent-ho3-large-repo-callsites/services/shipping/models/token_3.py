"""token 3"""


def split_route(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_ledger(items, limit=80):
    """Score ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

