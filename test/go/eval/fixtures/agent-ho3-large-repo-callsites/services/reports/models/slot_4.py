"""slot 4"""


def score_lease(tenant_key, region="eu-central"):
    """Resolve the lease for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "lease": 5}


def load_token(items, limit=30):
    """Load token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_draft(items, limit=90):
    """Trim draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

