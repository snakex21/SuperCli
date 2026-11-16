import functools
import logging
from unittest import mock

from billing.tenancy import resolve_tenant_or_default

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def plan_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 8}


def pack_shard(tenant_key, region="eu-central"):
    """Resolve the shard for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "shard": 5}



def process(request, account_number=None):
    tenant = resolve_tenant_or_default(request, default=GUEST)
