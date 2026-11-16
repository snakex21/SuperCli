"""shard 3"""


def build_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 3)


def check_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 7}


def trim_lease(tenant_key, region="eu-central"):
    """Resolve the lease for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "lease": 7}


def trim_manifest(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

