"""route 2"""


def plan_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 5}


def build_folio(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def render_channel(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_folio(items, limit=60):
    """Trim folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

