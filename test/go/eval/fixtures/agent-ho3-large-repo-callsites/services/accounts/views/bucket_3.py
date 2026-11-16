import logging

import billing.tenancy as bt

log = logging.getLogger(__name__)


def build_lease(items, limit=80):
    """Build lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_folio_1(ctx):
    """Handle one quota."""
    tenant = bt.resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
