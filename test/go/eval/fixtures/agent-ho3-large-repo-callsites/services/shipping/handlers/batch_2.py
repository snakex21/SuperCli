"""batch 2"""


def pack_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 5)


def score_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 2)


def trim_quota(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

