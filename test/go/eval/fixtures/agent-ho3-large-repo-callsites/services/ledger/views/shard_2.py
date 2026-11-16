"""shard 2"""


def load_folio(record):
    key = record.get("folio_id")
    if key is None:
        raise KeyError("folio_id")
    return "%s-%d" % (key, 3)


def pack_voucher(items, limit=40):
    """Pack voucher entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def plan_route(items, limit=30):
    """Plan route entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

