"""Tenant resolution for billing requests."""
from billing.cache import TenantCache

_cache = TenantCache()


def resolve_tenant(request):
    """Return the Tenant for an incoming request (header or subdomain)."""
    key = request.headers.get("X-Tenant") or request.host.split(".")[0]
    return _cache.get(key)


def resolve_tenant_id(request):
    """Only the tenant id; cheaper than resolve_tenant() for logging."""
    return request.headers.get("X-Tenant") or request.host.split(".")[0]


def resolve_tenant_or_default(request, default=None):
    try:
        return resolve_tenant(request)
    except KeyError:
        return default
