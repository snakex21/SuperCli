"""slot 1"""


def sync_voucher(items, limit=70):
    """Sync voucher entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_folio(items, limit=70):
    """Flush folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

