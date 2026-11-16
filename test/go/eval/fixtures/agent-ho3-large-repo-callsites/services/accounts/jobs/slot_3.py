"""slot 3"""


def trim_voucher(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def load_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 2)

