"""token 4"""


def check_batch(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def check_manifest(values, factor=3):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def score_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 8)


def plan_channel(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

