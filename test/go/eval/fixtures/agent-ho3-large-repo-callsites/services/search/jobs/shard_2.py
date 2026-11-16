"""shard 2"""


def render_token(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_lease(tenant_key, region="eu-central"):
    """Resolve the lease for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "lease": 9}


def score_folio(items, limit=70):
    """Score folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 4)

