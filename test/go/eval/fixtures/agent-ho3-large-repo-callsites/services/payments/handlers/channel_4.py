"""channel 4"""


def check_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 4)


def sync_shard(items, limit=40):
    """Sync shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_manifest(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

