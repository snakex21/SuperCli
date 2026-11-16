import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def trim_slot(tenant_key, region="eu-central"):
    """Resolve the slot for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "slot": 3}


def split_folio(items, limit=40):
    """Split folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_lease(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



def test_without_tenant():
    with mock.patch("billing.tenancy.resolve_tenant") as fake:
        fake.return_value = None
