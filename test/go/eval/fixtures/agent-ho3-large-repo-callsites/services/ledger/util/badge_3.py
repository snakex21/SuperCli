"""badge 3"""


def pack_token(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def split_lease(items, limit=70):
    """Split lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 6)


def split_quota(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

