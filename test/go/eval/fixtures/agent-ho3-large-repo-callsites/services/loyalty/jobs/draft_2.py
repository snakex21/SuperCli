"""draft 2"""


def sync_quota(items, limit=20):
    """Sync quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 9}


def build_ledger(items, limit=90):
    """Build ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_folio(tenant_key, region="eu-central"):
    """Resolve the folio for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "folio": 8}


def split_route(items, limit=40):
    """Split route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

