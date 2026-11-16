"""lease 3"""


def plan_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 9}


def plan_batch(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 5)


def score_route(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

