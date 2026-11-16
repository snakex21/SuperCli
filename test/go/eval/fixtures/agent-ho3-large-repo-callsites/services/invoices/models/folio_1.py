import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def split_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 8}


def trim_draft(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)



class TenantRouter:
    """Routes requests to shards; has its own resolve_tenant method."""

    def resolve_tenant(self, request):
        return request.shard

    def route(self, request):
        return self.resolve_tenant(request)
