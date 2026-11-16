import functools
import logging
from unittest import mock


log = logging.getLogger(__name__)
HOOKS = []
GUEST = None


def split_shard(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_folio(items, limit=60):
    """Sync folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def render_channel(tenant_key, region="eu-central"):
    """Resolve the channel for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "channel": 4}



def test_without_tenant():
    with mock.patch("billing.tenancy.resolve_tenant") as fake:
        fake.return_value = None
