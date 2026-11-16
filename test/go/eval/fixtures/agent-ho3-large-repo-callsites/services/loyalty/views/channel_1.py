"""channel 1"""


def build_badge(items, limit=80):
    """Build badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 3)


def sync_slot(items, limit=70):
    """Sync slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 2}


def load_batch(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

