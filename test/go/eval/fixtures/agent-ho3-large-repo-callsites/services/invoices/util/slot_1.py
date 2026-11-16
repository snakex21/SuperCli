"""slot 1"""


def merge_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 9}


def pack_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 4)


def trim_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 9)

