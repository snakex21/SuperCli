"""token 4"""


def sync_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 9}


def trim_bucket(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_ledger(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 8}

