"""slot 3"""


def flush_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 5)


def flush_window(items, limit=40):
    """Flush window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_shard(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def render_batch(items, limit=60):
    """Render batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_cursor(items, limit=30):
    """Load cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

