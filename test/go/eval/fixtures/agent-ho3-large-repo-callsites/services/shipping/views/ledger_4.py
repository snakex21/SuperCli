"""ledger 4"""


def merge_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 3}


def score_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 8)


def pack_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 4)

