import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def plan_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 4}


def flush_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 6)



class TenantRouter:
    """Routes requests to shards; has its own resolve_tenant method."""

    def resolve_tenant(self, request):
        return request.shard

    def route(self, request):
        return self.resolve_tenant(request)
