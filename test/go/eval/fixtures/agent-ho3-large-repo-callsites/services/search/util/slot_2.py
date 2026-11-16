"""slot 2"""


def load_bucket(items, limit=80):
    """Load bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 2}


def pack_cursor(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

