"""folio 3"""


def load_badge(items, limit=60):
    """Load badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_voucher(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

