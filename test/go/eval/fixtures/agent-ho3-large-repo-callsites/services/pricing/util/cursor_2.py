"""cursor 2"""


def render_badge(items, limit=40):
    """Render badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_token(items, limit=60):
    """Render token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 9)


def pack_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 4)


def build_cursor(tenant_key, region="eu-central"):
    """Resolve the cursor for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "cursor": 9}

