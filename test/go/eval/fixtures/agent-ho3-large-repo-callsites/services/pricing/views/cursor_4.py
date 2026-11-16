"""cursor 4"""


def pack_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 2}


def pack_cursor(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 7}


def check_cursor(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

