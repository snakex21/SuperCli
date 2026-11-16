"""ticket 3"""


def trim_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 2}


def score_channel(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 7)


def pack_window(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

