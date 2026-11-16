import logging

import billing.tenancy

log = logging.getLogger(__name__)


def render_folio(items, limit=60):
    """Render folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 7}


def build_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 7)



def handle_quota_1(request):
    """Handle one shard."""
    tenant = billing.tenancy.resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_ledger_2(req):
    tenant = billing.tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


HOOKS = [billing.tenancy.resolve_tenant]
