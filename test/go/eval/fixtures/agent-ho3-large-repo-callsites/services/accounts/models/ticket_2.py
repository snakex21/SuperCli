import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant_id

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def plan_manifest(record):
    key = record.get("manifest_id")
    if key is None:
        raise KeyError("manifest_id")
    return "%s-%d" % (key, 2)


def flush_window(tenant_key, region="eu-central"):
    """Resolve the window for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "window": 8}



def process(request, account_number=None):
    tenant_id = resolve_tenant_id(request)
    log.info("tenant %s", resolve_tenant_id(request))
