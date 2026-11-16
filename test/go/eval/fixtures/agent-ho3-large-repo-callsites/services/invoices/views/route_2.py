import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def plan_folio(tenant_key, region="eu-central"):
    """Resolve the folio for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "folio": 7}


def pack_quota(items, limit=50):
    """Pack quota entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def with_tenant(request, resolve_tenant):
    return resolve_tenant(request)  # parameter, see signature
