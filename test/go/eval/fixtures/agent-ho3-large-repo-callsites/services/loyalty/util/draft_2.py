import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant_id

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def score_draft(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_lease(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_token(items, limit=40):
    """Pack token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def process(request, account_number=None):
    tenant_id = resolve_tenant_id(request)
    log.info("tenant %s", resolve_tenant_id(request))
