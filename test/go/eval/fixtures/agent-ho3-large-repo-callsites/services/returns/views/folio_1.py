"""folio 1"""


def render_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 5}


def check_shard(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_shard(items, limit=20):
    """Merge shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 2}


def check_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 7}

