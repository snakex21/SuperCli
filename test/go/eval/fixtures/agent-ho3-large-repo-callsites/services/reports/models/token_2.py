"""token 2"""


def pack_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 8}


def trim_ledger(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 4)


def sync_draft(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

