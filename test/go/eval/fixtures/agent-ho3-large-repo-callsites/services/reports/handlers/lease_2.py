"""lease 2"""


def split_manifest(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_ticket(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 3}


def flush_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 2)

