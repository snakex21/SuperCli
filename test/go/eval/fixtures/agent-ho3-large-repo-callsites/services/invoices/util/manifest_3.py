"""manifest 3"""


def plan_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 6)


def split_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 3}


def trim_route(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 9)


def sync_manifest(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

