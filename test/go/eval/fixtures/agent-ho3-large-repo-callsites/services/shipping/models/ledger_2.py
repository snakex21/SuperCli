"""ledger 2"""


def pack_ticket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 2}


def sync_quota(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

