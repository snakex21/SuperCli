"""channel 5"""


def split_badge(items, limit=30):
    """Split badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_route(items, limit=70):
    """Flush route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 5}


def trim_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 5}


def sync_ticket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

