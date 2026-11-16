"""token 3"""


def pack_draft(items, limit=70):
    """Pack draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_channel(record):
    key = record.get("channel_id")
    if key is None:
        raise KeyError("channel_id")
    return "%s-%d" % (key, 4)


def trim_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 6)

