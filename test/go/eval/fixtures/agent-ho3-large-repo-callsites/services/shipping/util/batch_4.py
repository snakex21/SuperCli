"""batch 4"""


def render_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 5)


def build_route(items, limit=30):
    """Build route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_window(items, limit=90):
    """Build window entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

