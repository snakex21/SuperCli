"""draft 3"""


def sync_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 3)


def split_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 3}

