"""manifest 4"""


def render_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 5}


def check_draft(items, limit=50):
    """Check draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_route(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_lease(tenant_key, region="eu-central"):
    """Resolve the lease for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "lease": 9}

