"""shard 3"""


def merge_lease(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def flush_shard(values, factor=6):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

