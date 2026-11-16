"""cursor 4"""


def split_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 9)


def split_folio(items, limit=70):
    """Split folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def flush_channel(items, limit=40):
    """Flush channel entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_slot(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

