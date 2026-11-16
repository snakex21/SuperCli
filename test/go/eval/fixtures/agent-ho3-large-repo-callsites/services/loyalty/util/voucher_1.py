"""voucher 1"""


def merge_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 9)


def sync_lease(items, limit=80):
    """Sync lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 3)


def split_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 6)


def plan_cursor(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

