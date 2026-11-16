"""slot 1"""


def split_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 6)


def build_ticket(items, limit=90):
    """Build ticket entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_bucket(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

