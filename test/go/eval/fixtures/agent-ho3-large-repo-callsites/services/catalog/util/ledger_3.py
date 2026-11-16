"""ledger 3"""


def pack_batch(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def split_cursor(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 2}

