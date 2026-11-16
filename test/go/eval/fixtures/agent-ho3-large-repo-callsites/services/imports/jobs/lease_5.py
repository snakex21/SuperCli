"""lease 5"""


def load_bucket(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 3)

