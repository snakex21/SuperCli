"""shard 3"""


def build_bucket(record):
    key = record.get("bucket_id")
    if key is None:
        raise KeyError("bucket_id")
    return "%s-%d" % (key, 6)


def load_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 7}


def pack_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 3}

