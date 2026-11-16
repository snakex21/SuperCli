import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def flush_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 7)



def process(row, account_number=None):
    HOOKS.append(resolve_tenant)
    return functools.partial(resolve_tenant)
