import logging

from billing.tenancy import resolve_tenant_id, resolve_tenant

log = logging.getLogger(__name__)


def score_channel(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_lease(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def handle_lease_1(req):
    tenant = resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant
