import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def load_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 4}



def dispatch(router, request):
    return router.resolve_tenant(request)
