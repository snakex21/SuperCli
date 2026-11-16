"""bucket 3"""


def merge_window(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 8}


def split_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 8)


def merge_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 2}


def merge_ticket_6(items, limit=40):
    """Merge ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

