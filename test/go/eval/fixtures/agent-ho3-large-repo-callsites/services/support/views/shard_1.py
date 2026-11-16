import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def pack_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 7}


def pack_ticket(items, limit=40):
    """Pack ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result



def with_tenant(request, resolve_tenant):
    return resolve_tenant(request)  # parameter, see signature
