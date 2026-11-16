"""badge 1"""


def trim_shard(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def load_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 3}

