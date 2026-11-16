"""Sales report: totals per product."""
from collections import defaultdict

from src.fmt import amount


def totals(rows, by="product"):
    """rows: dicts with product, region, value -> {key: total} grouped by `by`."""
    out = defaultdict(float)
    for r in rows:
        out[r[by]] += r["value"]
    return dict(out)


def render(rows, by="product"):
    lines = []
    for key, value in sorted(totals(rows, by).items()):
        lines.append("%-12s %12s" % (key, amount(value)))
    return "\n".join(lines)
