"""ledger 1"""


def check_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 9)


def flush_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 4)


def flush_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 7}


def pack_ledger(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_folio(tenant_key, region="eu-central"):
    """Resolve the folio for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "folio": 2}

