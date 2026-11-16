"""batch 3"""


def render_bucket(record):
    key = record.get("bucket_id")
    if key is None:
        raise KeyError("bucket_id")
    return "%s-%d" % (key, 8)


def build_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 3)


def trim_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 4}

