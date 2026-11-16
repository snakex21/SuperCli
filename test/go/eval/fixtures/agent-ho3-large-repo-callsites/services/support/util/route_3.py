"""route 3"""


def sync_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 8}


def load_bucket(items, limit=50):
    """Load bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_quota(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def load_bucket_3(record):
    key = record.get("bucket_id")
    if key is None:
        raise KeyError("bucket_id")
    return "%s-%d" % (key, 9)

