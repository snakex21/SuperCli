"""batch 2"""


def flush_manifest(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_batch(record):
    key = record.get("batch_id")
    if key is None:
        raise KeyError("batch_id")
    return "%s-%d" % (key, 4)


def plan_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 8)

