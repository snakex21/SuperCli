"""cursor 4"""


def load_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 5)


def check_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 5)


def trim_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 3)


def plan_shard(items, limit=90):
    """Plan shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

