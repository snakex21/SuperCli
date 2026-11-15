"""Monthly station report: per-station totals computed with sheet formulas.

Input CSV: header row ("station", day 1, day 2, ...), then one row per
station with that day's ride count in every following column.
"""

from sheetcalc.csvio import load_csv
from sheetcalc.engine import cell_name


def summarize(path):
    """{station: (total, average rounded to 2 places, best day)} plus the grand total.

    The best day is the first day with the highest count (1-based).
    """
    sheet = load_csv(path)
    rows, cols = sheet.bounds()
    first, last = cell_name(0, 1)[:-1], cell_name(0, cols - 1)[:-1]
    out = {}
    for r in range(1, rows):
        span = "%s%d:%s%d" % (first, r + 1, last, r + 1)
        sheet.set_rc(r, cols, "=SUM(%s)" % span)
        sheet.set_rc(r, cols + 1, "=ROUND(AVERAGE(%s), 2)" % span)
        sheet.set_rc(r, cols + 2, "=MATCH(MAX(%s), %s, 0)" % (span, span))
    total_col = cell_name(0, cols)[:-1]
    sheet.set_rc(rows, cols, "=SUM(%s2:%s%d)" % (total_col, total_col, rows))
    for r in range(1, rows):
        station = sheet.value(r, 0)
        out[station] = (int(sheet.value(r, cols)), sheet.value(r, cols + 1), int(sheet.value(r, cols + 2)))
    return out, int(sheet.value(rows, cols))


def render(path):
    stations, total = summarize(path)
    lines = ["%-12s %7s %8s %4s" % ("station", "total", "avg", "best")]
    for name in sorted(stations):
        tot, avg, best = stations[name]
        lines.append("%-12s %7d %8.2f %4d" % (name, tot, avg, best))
    lines.append("%-12s %7d" % ("ALL", total))
    return "\n".join(lines) + "\n"
