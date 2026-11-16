"""draft 5"""


def build_voucher(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_bucket(tenant_key, region="eu-central"):
    """Resolve the bucket for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "bucket": 3}


def trim_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 8)

