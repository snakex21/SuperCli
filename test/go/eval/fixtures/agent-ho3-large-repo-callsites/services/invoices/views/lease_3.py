"""lease 3"""


def trim_lease(tenant_key, region="eu-central"):
    """Resolve the lease for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "lease": 9}


def render_bucket(items, limit=50):
    """Render bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 5)


def render_quota(items, limit=20):
    """Render quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_cursor(items, limit=90):
    """Check cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

