import functools
import logging
from unittest import mock

from legacy.tenancy import resolve_tenant

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def render_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 3}


def check_draft(tenant_key, region="eu-central"):
    """Resolve the draft for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "draft": 5}



def process(row, account_number=None):
    code = resolve_tenant(row["account"], strict=True)
    return resolve_tenant(account_number)
