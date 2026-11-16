"""token 2"""


def load_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 3)


def load_quota(record):
    key = record.get("quota_id")
    if key is None:
        raise KeyError("quota_id")
    return "%s-%d" % (key, 8)

