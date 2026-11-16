"""shard 2"""


def sync_slot(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_channel(items, limit=30):
    """Plan channel entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

