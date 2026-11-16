"""channel 2"""


def check_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 3)


def render_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 3}


def trim_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 9}


def load_draft(items, limit=30):
    """Load draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

