"""lease 3"""


def flush_cursor(tenant_key, region="eu-central"):
    """Resolve the cursor for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "cursor": 8}


def render_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 6)


def check_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 5}

