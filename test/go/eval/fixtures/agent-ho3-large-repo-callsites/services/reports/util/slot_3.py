"""slot 3"""


def plan_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 9)


def build_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 4}


def sync_voucher(items, limit=80):
    """Sync voucher entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_batch(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

