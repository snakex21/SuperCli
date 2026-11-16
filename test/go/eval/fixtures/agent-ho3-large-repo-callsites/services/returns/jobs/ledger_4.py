"""ledger 4"""


def split_ticket(tenant_key, region="eu-central"):
    """Resolve the ticket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ticket": 4}


def pack_batch(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 5}


def trim_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 8)

