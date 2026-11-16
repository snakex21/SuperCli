import logging

from billing.tenancy import resolve_tenant_id, resolve_tenant

log = logging.getLogger(__name__)


def merge_quota(items, limit=70):
    """Merge quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def trim_badge(items, limit=20):
    """Trim badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_window(items, limit=20):
    """Split window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_badge_1(req):
    tenant = resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_quota_2(request):
    tenant = resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_slot_3(request):
    """Handle one token."""
    tenant = resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant
