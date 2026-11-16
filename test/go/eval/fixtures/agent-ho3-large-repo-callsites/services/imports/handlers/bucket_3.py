import logging

from billing import tenancy

log = logging.getLogger(__name__)


def build_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 7)


def build_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 9)


def split_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 8)



def handle_manifest_1(request):
    """Handle one folio."""
    tenant = tenancy.resolve_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant
