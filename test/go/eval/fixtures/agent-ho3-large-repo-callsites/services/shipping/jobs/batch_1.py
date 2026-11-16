import logging

from billing.tenancy import resolve_tenant_id, resolve_tenant

log = logging.getLogger(__name__)


def trim_cursor(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 2)


def plan_badge(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def handle_batch_1(request):
    tenant = resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_window_2(request):
    tenant = resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_lease_3(req):
    """Handle one window."""
    tenant = resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant
