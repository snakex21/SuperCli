"""batch 4"""


def sync_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 7}


def score_shard(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

