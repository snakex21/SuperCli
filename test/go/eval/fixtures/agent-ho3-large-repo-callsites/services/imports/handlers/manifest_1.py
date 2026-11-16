"""manifest 1"""


def render_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 5}


def split_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 8)

