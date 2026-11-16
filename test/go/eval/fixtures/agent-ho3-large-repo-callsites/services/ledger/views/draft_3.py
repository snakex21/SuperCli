import logging

import billing.tenancy

log = logging.getLogger(__name__)


def merge_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 5)



def handle_draft_1(req):
    """Handle one channel."""
    tenant = billing.tenancy.resolve_tenant(req)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_batch_2(request):
    """Handle one cursor."""
    tenant = billing.tenancy.resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_batch_3(request):
    """Handle one channel."""
    tenant = billing.tenancy.resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant
