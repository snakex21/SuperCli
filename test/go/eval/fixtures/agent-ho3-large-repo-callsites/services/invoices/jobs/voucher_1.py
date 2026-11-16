"""voucher 1"""


def build_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 2)


def merge_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 3)


def sync_ledger(values, factor=2):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_badge(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

