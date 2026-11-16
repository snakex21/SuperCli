"""channel 3"""


def plan_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 7}


def plan_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 8)


def pack_slot(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_ledger(items, limit=20):
    """Score ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 3}

