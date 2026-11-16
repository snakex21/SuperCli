"""voucher 3"""


def build_ledger(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_slot(items, limit=90):
    """Flush slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_token(items, limit=30):
    """Build token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_ledger(items, limit=20):
    """Load ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

