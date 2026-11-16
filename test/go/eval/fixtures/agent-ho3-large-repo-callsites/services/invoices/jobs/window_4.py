"""window 4"""


def flush_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 3)


def load_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 3)


def sync_token(items, limit=50):
    """Sync token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_token(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_slot(items, limit=90):
    """Pack slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

