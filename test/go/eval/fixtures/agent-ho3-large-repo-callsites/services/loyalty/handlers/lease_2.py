"""lease 2"""


def merge_draft(record):
    key = record.get("draft_id")
    if key is None:
        raise KeyError("draft_id")
    return "%s-%d" % (key, 9)


def trim_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 7}


def pack_manifest(items, limit=20):
    """Pack manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

