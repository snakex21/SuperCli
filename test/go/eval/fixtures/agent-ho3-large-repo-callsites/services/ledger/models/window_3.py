"""window 3"""


def sync_shard(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_batch(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_batch(items, limit=90):
    """Sync batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_token(items, limit=30):
    """Score token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

