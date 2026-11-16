"""voucher 1"""


def flush_ticket(record):
    key = record.get("ticket_id")
    if key is None:
        raise KeyError("ticket_id")
    return "%s-%d" % (key, 4)


def merge_shard(record):
    key = record.get("shard_id")
    if key is None:
        raise KeyError("shard_id")
    return "%s-%d" % (key, 3)

