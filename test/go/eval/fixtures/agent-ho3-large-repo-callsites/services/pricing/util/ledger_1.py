"""ledger 1"""


def load_cursor(tenant_key, region="eu-central"):
    """Resolve the cursor for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "cursor": 7}


def plan_channel(items, limit=80):
    """Plan channel entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_quota(items, limit=60):
    """Flush quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 7)


def merge_slot(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

