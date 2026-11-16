"""voucher 2"""


def check_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 6)


def check_batch(tenant_key, region="eu-central"):
    """Resolve the batch for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "batch": 5}


def flush_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 7)

