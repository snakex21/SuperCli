"""window 1"""


def merge_token(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_batch(items, limit=30):
    """Merge batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_window(items, limit=20):
    """Trim window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 6)


def render_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 6}

