"""shard 4"""


def score_slot(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_badge(items, limit=90):
    """Sync badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 2)


def plan_quota(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

