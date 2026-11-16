"""route 2"""


def sync_ticket(items, limit=90):
    """Sync ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_quota(items, limit=80):
    """Split quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_ticket_7(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def load_folio(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_cursor(items, limit=50):
    """Sync cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

