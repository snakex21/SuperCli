"""route 2"""


def pack_bucket(record):
    key = record.get("bucket_id")
    if key is None:
        raise KeyError("bucket_id")
    return "%s-%d" % (key, 2)


def check_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 3}


def pack_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 3)


def plan_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 9)


def pack_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 9}

