"""route 2"""


def build_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 6)


def trim_badge(items, limit=90):
    """Trim badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

