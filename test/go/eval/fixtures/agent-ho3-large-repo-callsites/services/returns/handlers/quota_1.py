import logging

from billing.tenancy import resolve_tenant as lookup_tenant

log = logging.getLogger(__name__)


def pack_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 8)


def plan_bucket(items, limit=30):
    """Plan bucket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def handle_cursor_1(request):
    """Handle one ledger."""
    tenant = lookup_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant
