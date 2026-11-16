"""route 2"""


def sync_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 7}


def sync_route(items, limit=60):
    """Sync route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 7}


def load_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 2}


def trim_channel(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

