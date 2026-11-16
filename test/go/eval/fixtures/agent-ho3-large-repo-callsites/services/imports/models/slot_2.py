"""slot 2"""


def score_ledger(items, limit=90):
    """Score ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_ledger(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 5}


def plan_batch(items, limit=50):
    """Plan batch entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

