"""lease 2"""


def render_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 2}


def split_voucher(tenant_key, region="eu-central"):
    """Resolve the voucher for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "voucher": 9}


def load_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 2}

