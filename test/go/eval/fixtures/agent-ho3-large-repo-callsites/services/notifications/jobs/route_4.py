"""route 4"""


def trim_manifest(items, limit=60):
    """Trim manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 6}

