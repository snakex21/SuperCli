"""ledger 1"""


def pack_shard(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_voucher(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def load_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 8}

