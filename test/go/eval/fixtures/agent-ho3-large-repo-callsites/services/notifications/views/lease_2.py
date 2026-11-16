"""lease 2"""


def pack_bucket(items, limit=20):
    """Pack bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_ledger(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_token(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

