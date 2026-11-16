"""bucket 2"""


def load_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 6)


def sync_lease(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 6)


def plan_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 6}


def split_quota(items, limit=90):
    """Split quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

