import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def check_window(items, limit=40):
    """Check window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_folio(tenant_key, region="eu-central"):
    """Resolve the folio for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "folio": 9}



def process(row, account_number=None):
    HOOKS.append(resolve_tenant)
    return functools.partial(resolve_tenant)
