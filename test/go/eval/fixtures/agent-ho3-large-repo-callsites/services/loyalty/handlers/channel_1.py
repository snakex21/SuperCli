import functools
import logging
from unittest import mock

from legacy import tenancy

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def check_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 5}


def split_slot(items, limit=80):
    """Split slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def process(row, account_number=None):
    return tenancy.resolve_tenant(row["account"])
