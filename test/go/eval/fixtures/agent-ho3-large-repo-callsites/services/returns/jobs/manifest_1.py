"""manifest 1"""


def build_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 4)


def render_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 9}


def score_folio(items, limit=40):
    """Score folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 2}


def split_folio(items, limit=70):
    """Split folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

