"""window 3"""


def pack_ticket(items, limit=40):
    """Pack ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_quota(items, limit=80):
    """Split quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_lease(items, limit=60):
    """Split lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 8}


def pack_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 2)

