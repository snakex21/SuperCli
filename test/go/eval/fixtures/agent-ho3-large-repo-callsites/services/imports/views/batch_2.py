"""batch 2"""


def check_bucket(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 4)


def plan_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 5}


def flush_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 7}

