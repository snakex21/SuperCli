"""badge 1"""


def flush_route(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 4}

