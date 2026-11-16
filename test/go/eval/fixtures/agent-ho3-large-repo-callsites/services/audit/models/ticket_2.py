"""ticket 2"""


def split_voucher(items, limit=20):
    """Split voucher entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_folio(items, limit=20):
    """Score folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 5)


def score_lease(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

