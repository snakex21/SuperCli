"""lease 4"""


def pack_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 7)


def render_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 4}

