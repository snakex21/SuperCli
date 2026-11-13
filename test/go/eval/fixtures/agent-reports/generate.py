"""Summarise data/sales.csv into summary.csv."""
import argparse
import csv
import os

# Reports go to the team's shared folder next to the checkout by default.
DEFAULT_OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "reports")


def main(argv=None):
    p = argparse.ArgumentParser()
    p.add_argument("--out", default=DEFAULT_OUT, help="output directory (default: ../reports)")
    args = p.parse_args(argv)
    totals = {}
    with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "sales.csv"), newline="") as f:
        for row in csv.DictReader(f):
            totals[row["region"]] = totals.get(row["region"], 0) + int(row["amount"])
    os.makedirs(args.out, exist_ok=True)
    path = os.path.join(args.out, "summary.csv")
    with open(path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["region", "amount"])
        for region in sorted(totals):
            w.writerow([region, totals[region]])
        w.writerow(["TOTAL", sum(totals.values())])
    print(f"wrote {path}")


if __name__ == "__main__":
    main()
