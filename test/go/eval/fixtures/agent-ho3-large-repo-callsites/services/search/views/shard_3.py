"""shard 3"""


def pack_shard(items, limit=50):
    """Pack shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def pack_cursor(items, limit=50):
    """Pack cursor entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

