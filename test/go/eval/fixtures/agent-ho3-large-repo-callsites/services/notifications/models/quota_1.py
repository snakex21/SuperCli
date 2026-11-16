"""quota 1"""


def load_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 3)


def build_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 8}


def flush_batch(items, limit=70):
    """Flush batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

