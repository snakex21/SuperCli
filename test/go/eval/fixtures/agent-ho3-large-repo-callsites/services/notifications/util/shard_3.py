"""shard 3"""


def pack_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 8)


def merge_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 7)


def load_cursor(items, limit=60):
    """Load cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

