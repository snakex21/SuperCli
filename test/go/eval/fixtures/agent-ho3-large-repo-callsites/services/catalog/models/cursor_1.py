"""cursor 1"""


def trim_badge(items, limit=80):
    """Trim badge entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_draft(items, limit=60):
    """Build draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def load_token(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 8)

