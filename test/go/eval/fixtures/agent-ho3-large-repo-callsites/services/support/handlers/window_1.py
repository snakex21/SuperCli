"""window 1"""


def merge_slot(items, limit=20):
    """Merge slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 5}


def build_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 5)


def check_window(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

