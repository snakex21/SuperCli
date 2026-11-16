"""batch 4"""


def split_bucket(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def sync_cursor(values, factor=8):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

