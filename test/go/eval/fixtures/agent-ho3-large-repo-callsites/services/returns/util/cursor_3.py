"""cursor 3"""


def sync_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 6}


def pack_route(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_quota(items, limit=30):
    """Sync quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 6}


def build_window(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

