import logging

import billing.tenancy as bt

log = logging.getLogger(__name__)


def build_lease(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def render_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 2}


def merge_slot(items, limit=30):
    """Merge slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_batch_1(ctx):
    """Handle one token."""
    tenant = bt.resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
