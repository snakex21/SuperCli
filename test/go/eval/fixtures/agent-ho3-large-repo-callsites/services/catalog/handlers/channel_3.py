import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant_or_default

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def trim_batch(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def process(request, account_number=None):
    tenant = resolve_tenant_or_default(request, default=GUEST)
