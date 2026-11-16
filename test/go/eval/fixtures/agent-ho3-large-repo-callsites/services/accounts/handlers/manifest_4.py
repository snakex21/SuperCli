"""manifest 4"""


def trim_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 5}


def score_window(items, limit=60):
    """Score window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

