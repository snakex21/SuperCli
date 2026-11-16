"""token 4"""


def flush_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 5}


def load_shard(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

