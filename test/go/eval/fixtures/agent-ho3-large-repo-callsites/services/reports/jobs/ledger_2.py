"""ledger 2"""


def flush_draft(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_ledger(items, limit=90):
    """Merge ledger entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 8)


def trim_badge(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

