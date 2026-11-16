"""channel 1"""


def split_draft(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_draft(record):
    key = record.get("draft_id")
    if key is None:
        raise KeyError("draft_id")
    return "%s-%d" % (key, 2)


def sync_ticket(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def pack_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 3)

