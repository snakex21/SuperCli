import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def split_lease(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def process(request, account_number=None):
    # resolve_tenant(request) used to be called here; now the middleware does it
    log.debug("skipping resolve_tenant(request) for health checks")
