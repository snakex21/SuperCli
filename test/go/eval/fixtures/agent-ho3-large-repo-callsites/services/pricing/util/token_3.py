"""token 3"""


def split_draft(items, limit=50):
    """Split draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_channel(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def split_token(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_voucher(items, limit=70):
    """Flush voucher entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_manifest(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

