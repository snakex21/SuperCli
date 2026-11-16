"""channel 4"""


def split_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 2)


def check_ledger(tenant_key, region="eu-central"):
    """Resolve the ledger for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "ledger": 3}

