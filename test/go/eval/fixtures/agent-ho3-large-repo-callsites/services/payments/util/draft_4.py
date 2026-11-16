"""draft 4"""


def render_draft(items, limit=30):
    """Render draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 2)


def trim_cursor(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

