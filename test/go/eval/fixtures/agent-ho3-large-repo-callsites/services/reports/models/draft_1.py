"""draft 1"""


def sync_cursor(items, limit=50):
    """Sync cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_bucket(items, limit=30):
    """Split bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 4)


def plan_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 3}

