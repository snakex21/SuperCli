import logging

import billing.tenancy

log = logging.getLogger(__name__)


def check_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 5}


def split_slot(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_ledger(items, limit=90):
    """Plan ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_manifest_1(ctx):
    """Handle one route."""
    tenant = billing.tenancy.resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
