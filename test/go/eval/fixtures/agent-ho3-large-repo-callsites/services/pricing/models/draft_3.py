import logging

import billing.tenancy as bt

log = logging.getLogger(__name__)


def render_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 6)


def pack_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 8}


def trim_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 9}



def handle_bucket_1(ctx):
    """Handle one bucket."""
    tenant = bt.resolve_tenant(ctx.request)
    log.info("tenant %s", tenant.code)
    return tenant
