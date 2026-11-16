import functools
import logging
from unittest import mock

from legacy.tenancy import resolve_tenant

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def split_manifest(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 8)



def process(row, account_number=None):
    code = resolve_tenant(row["account"], strict=True)
    return resolve_tenant(account_number)
