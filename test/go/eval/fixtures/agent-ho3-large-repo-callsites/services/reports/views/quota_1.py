"""quota 1"""


def merge_ticket(values, factor=5):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def merge_cursor(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

