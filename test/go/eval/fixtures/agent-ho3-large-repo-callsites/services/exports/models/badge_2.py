"""badge 2"""


def trim_cursor(tenant_key, region="eu-central"):
    """Resolve the cursor for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "cursor": 9}


def trim_lease(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

