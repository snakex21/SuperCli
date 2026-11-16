"""ledger 3"""


def build_window(items, limit=30):
    """Build window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_token(items, limit=50):
    """Sync token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 5}


def sync_ticket(items, limit=80):
    """Sync ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

