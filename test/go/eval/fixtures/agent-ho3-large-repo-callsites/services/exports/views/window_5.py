import logging

from billing.tenancy import resolve_tenant

log = logging.getLogger(__name__)


def sync_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 9}


def score_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 3}



def handle_window_1(ctx):
    """Handle one lease."""
    tenant = resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
