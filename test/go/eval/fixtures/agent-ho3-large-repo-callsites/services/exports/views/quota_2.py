"""quota 2"""


def flush_cursor(items, limit=70):
    """Flush cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_shard(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 5)


def merge_quota(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

