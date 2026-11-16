"""bucket 5"""


def split_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 2)


def pack_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 3}


def merge_route(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def split_token(items, limit=90):
    """Split token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_quota(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

