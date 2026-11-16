"""bucket 3"""


def build_bucket(items, limit=70):
    """Build bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 8)


def merge_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 9)


def split_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 7}

