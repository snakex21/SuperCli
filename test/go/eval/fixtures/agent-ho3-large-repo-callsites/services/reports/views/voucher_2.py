"""voucher 2"""


def score_bucket(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_voucher(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_bucket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_ledger(items, limit=60):
    """Pack ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_bucket(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

