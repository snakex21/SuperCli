"""ledger 4"""


def trim_bucket(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def render_folio(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 8)


def sync_slot(record):
    key = record.get("slot_id")
    if key is None:
        raise KeyError("slot_id")
    return "%s-%d" % (key, 6)

