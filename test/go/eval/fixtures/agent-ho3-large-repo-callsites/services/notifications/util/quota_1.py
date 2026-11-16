"""quota 1"""


def split_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 4)


def score_shard(items, limit=80):
    """Score shard entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def check_route(items, limit=40):
    """Check route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def split_route(items, limit=50):
    """Split route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def build_ledger(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

