"""folio 3"""


def trim_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 7}


def trim_manifest_8(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 3)


def load_window(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_quota(tenant_key, region="eu-central"):
    """Resolve the quota for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "quota": 6}

