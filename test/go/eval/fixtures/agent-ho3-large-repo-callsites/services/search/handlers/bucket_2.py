"""bucket 2"""


def render_voucher(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_ticket(items, limit=70):
    """Score ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_window(items, limit=20):
    """Plan window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

