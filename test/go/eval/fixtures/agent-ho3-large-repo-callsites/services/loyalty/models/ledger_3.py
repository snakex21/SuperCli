"""ledger 3"""


def render_lease(items, limit=30):
    """Render lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 2)


def split_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 8}


def plan_batch(items, limit=40):
    """Plan batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 3)

