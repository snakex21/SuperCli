"""window 1"""


def plan_cursor(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def build_route(record):
    key = record.get("route_id")
    if key is None:
        raise KeyError("route_id")
    return "%s-%d" % (key, 2)

