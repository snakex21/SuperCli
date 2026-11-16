"""channel 5"""


def merge_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 8)


def trim_shard(items, limit=80):
    """Trim shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_manifest(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_batch(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_cursor(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

