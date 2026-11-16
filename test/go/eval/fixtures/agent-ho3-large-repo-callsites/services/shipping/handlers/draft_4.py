"""draft 4"""


def sync_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 7)


def score_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 8)


def sync_cursor(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_draft(items, limit=30):
    """Plan draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_badge(tenant_key, region="eu-central"):
    """Resolve the badge for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "badge": 8}

