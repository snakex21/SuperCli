"""ticket 1"""


def render_ticket(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 7}


def trim_folio(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 2}

