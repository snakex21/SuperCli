import logging

from billing.tenancy import resolve_tenant as lookup_tenant

log = logging.getLogger(__name__)


def score_shard(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_token(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 2)



def handle_token_1(request):
    tenant = lookup_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant


def handle_batch_2(request):
    tenant = lookup_tenant(request)
    log.info("tenant %s", tenant.code)
    return tenant
