import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def load_channel(items, limit=20):
    """Load channel entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_lease(items, limit=40):
    """Plan lease entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_manifest(tenant_key, region="eu-central"):
    """Resolve the manifest for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "manifest": 9}



def process(request, account_number=None):
    # resolve_tenant(request) used to be called here; now the middleware does it
    log.debug("skipping resolve_tenant(request) for health checks")
