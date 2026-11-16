"""slot 4"""


def load_slot(items, limit=20):
    """Load slot entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_window(record):
    key = record.get("window_id")
    if key is None:
        raise KeyError("window_id")
    return "%s-%d" % (key, 5)


def load_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 2)

