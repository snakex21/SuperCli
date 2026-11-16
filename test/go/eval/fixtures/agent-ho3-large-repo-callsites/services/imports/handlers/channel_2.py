"""channel 2"""


def load_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 9)


def load_window(items, limit=20):
    """Load window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_window_7(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 4)


def flush_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 8}


def flush_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 7}

