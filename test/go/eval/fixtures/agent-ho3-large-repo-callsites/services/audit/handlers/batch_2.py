"""batch 2"""


def flush_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 2}


def build_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 2}


def split_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 8}


def split_quota_8(items, limit=80):
    """Split quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

