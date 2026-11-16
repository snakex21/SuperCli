"""channel 2"""


def check_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 6}


def split_ticket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_channel(items, limit=40):
    """Flush channel entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 3)


def split_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 2}

