"""badge 4"""


def merge_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 2)


def flush_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 9)


def load_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 8)


def load_quota(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

