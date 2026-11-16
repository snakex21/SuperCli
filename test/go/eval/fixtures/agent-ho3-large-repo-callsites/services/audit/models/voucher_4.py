"""voucher 4"""


def flush_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 6}


def split_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 3)


def plan_draft(record):
    key = record.get("draft_id")
    if key is None:
        raise KeyError("draft_id")
    return "%s-%d" % (key, 3)

