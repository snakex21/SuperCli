"""batch 2"""


def sync_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 3)


def check_badge(items, limit=30):
    """Check badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_ticket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 6)


def plan_ticket(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

