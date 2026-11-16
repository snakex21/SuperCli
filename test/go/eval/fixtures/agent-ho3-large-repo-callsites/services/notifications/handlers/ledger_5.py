"""ledger 5"""


def check_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 3)


def flush_route(items, limit=20):
    """Flush route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def merge_route(items, limit=70):
    """Merge route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

