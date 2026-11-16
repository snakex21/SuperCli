"""folio 2"""


def merge_ticket(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 5)


def pack_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 4)


def load_voucher(record):
    key = record.get("voucher_id")
    if key is None:
        raise KeyError("voucher_id")
    return "%s-%d" % (key, 7)

