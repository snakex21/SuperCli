"""bucket 1"""


def flush_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 8)


def split_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 6}


def plan_lease(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_route(items, limit=70):
    """Trim route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 8)

