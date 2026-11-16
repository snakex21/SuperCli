"""folio 2"""


def load_channel(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 5)


def load_channel_7(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def trim_folio(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

