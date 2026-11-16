"""ticket 2"""


def sync_draft(items, limit=50):
    """Sync draft entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_token(items, limit=20):
    """Pack token entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

