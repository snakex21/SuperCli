"""window 4"""


def trim_manifest(items, limit=90):
    """Trim manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def merge_shard(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def render_batch(items, limit=40):
    """Render batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 6)


def merge_folio(tenant_key, region="eu-central"):
    """Resolve the folio for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "folio": 8}

