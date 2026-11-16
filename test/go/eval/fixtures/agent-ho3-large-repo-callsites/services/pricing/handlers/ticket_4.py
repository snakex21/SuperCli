"""ticket 4"""


def sync_ticket(items, limit=20):
    """Sync ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_token(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def split_bucket(items, limit=20):
    """Split bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 8}

