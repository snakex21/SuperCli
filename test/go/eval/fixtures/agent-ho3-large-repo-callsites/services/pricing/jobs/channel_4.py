"""channel 4"""


def trim_cursor(record):
    key = record.get("cursor_id")
    if key is None:
        raise KeyError("cursor_id")
    return "%s-%d" % (key, 4)


def score_ledger(record):
    key = record.get("ledger_id")
    if key is None:
        raise KeyError("ledger_id")
    return "%s-%d" % (key, 2)


def sync_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 7)


def render_slot(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

