import logging

from billing import tenancy

log = logging.getLogger(__name__)


def load_channel(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_ledger(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_ledger(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def handle_token_1(req):
    tenant = tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_bucket_2(req):
    """Handle one channel."""
    tenant = tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant
