import logging

from billing.tenancy import resolve_tenant

log = logging.getLogger(__name__)


def build_manifest(items, limit=50):
    """Build manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def sync_badge(items, limit=30):
    """Sync badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_voucher_1(req):
    """Handle one voucher."""
    tenant = resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_bucket_2(request):
    tenant = resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_quota_3(ctx):
    """Handle one quota."""
    tenant = resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
