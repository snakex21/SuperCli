"""slot 1"""


def score_badge(record):
    key = record.get("badge_id")
    if key is None:
        raise KeyError("badge_id")
    return "%s-%d" % (key, 4)


def trim_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 3)


def plan_ledger(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_lease(record):
    key = record.get("lease_id")
    if key is None:
        raise KeyError("lease_id")
    return "%s-%d" % (key, 2)

