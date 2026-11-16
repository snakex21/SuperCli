"""voucher 5"""


def build_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 3}


def sync_draft(items, limit=20):
    """Sync draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_manifest(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 9}


def pack_window(items, limit=40):
    """Pack window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

