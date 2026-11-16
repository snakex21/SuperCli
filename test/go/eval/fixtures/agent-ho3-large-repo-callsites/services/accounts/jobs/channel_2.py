import functools
import logging
from unittest import mock

from legacy import tenancy

log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def pack_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 4}



def process(row, account_number=None):
    return tenancy.resolve_tenant(row["account"])
