"""channel 2"""


def flush_route(items, limit=40):
    """Flush route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_window(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_badge(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_cursor(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

