"""quota 1"""


def score_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 6)


def split_cursor(tenant_key, region="eu-central"):
    """Resolve the cursor for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "cursor": 4}


def render_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 4)

