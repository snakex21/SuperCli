"""folio 1"""


def render_lease(items, limit=80):
    """Render lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 3}


def flush_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 3)

