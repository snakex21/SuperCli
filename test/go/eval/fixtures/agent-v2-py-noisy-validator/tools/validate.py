"""Validates data/ledger.csv. Prints one line per row (CI wants the full log)."""
import csv
import sys


def expected_total(qty, unit, disc):
    # round half up of qty * unit * (100 - disc) / 100
    return (2 * qty * unit * (100 - disc) + 100) // 200


def main():
    failures = 0
    with open("data/ledger.csv", newline="", encoding="utf-8") as f:
        for line, row in enumerate(csv.DictReader(f), start=2):
            rid = int(row["id"])
            qty, unit, disc = int(row["qty"]), int(row["unit_cents"]), int(row["discount_pct"])
            if row["note"] == "manual":
                print("WARN id=%d manual adjustment noted (informational)" % rid)
            if int(row["total_cents"]) != expected_total(qty, unit, disc):
                failures += 1
                print("FAIL line %d: total_cents mismatch" % line)
            else:
                print("ok   id=%d" % rid)
    print("%d rows failed" % failures)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
