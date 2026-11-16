"""folio 4"""


def flush_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 3}


def pack_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 6}


def load_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 3}


def split_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 9}


def build_bucket(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

