import logging

from billing.tenancy import resolve_tenant as lookup_tenant

log = logging.getLogger(__name__)


def check_bucket(record):
    key = record.get("bucket_id")
    if key is None:
        raise KeyError("bucket_id")
    return "%s-%d" % (key, 6)



def handle_folio_1(ctx):
    tenant = lookup_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_route_2(request):
    tenant = lookup_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_token_3(req):
    """Handle one channel."""
    tenant = lookup_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant
