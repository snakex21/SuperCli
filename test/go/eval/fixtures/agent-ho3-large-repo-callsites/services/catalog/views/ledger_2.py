"""ledger 2"""


def sync_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 3}


def split_batch(items, limit=60):
    """Split batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_token(items, limit=50):
    """Flush token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_cursor(items, limit=80):
    """Plan cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def merge_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 7}

