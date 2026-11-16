import logging

from billing import tenancy

log = logging.getLogger(__name__)


def score_folio(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def handle_window_1(req):
    """Handle one token."""
    tenant = tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_channel_2(req):
    """Handle one shard."""
    tenant = tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_manifest_3(ctx):
    """Handle one cursor."""
    tenant = tenancy.resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
