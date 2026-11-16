"""quota 3"""


def check_batch(values, factor=7):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)


def plan_folio(values, factor=9):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

