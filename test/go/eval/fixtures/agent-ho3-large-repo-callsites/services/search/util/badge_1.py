"""badge 1"""


def trim_slot(items, limit=70):
    """Trim slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 2}


def check_window(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

