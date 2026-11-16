"""quota 5"""


def split_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 3)


def split_route(items, limit=80):
    """Split route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_window(items, limit=90):
    """Check window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

