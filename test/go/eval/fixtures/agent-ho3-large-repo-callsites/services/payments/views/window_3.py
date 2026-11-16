"""window 3"""


def sync_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 4}


def trim_bucket(items, limit=20):
    """Trim bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_shard(items, limit=90):
    """Plan shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_lease(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 8)

