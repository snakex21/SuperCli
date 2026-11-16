import logging

from billing.tenancy import resolve_tenant

log = logging.getLogger(__name__)


def split_folio(items, limit=70):
    """Split folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_ledger_1(req):
    tenant = resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_window_2(ctx):
    """Handle one manifest."""
    tenant = resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_draft_3(ctx):
    tenant = resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
