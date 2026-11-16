"""slot 4"""


def merge_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 5}


def merge_ledger(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

