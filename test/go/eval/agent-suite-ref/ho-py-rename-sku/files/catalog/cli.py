"""catalog command line: run a pipeline over a CSV feed."""

import argparse
import csv
import sys

from . import pipeline
from .transforms import TRANSFORMS


def build_parser():
    p = argparse.ArgumentParser(prog="catalog")
    p.add_argument("feed", help="CSV feed with a 'sku' column")
    p.add_argument("--pipeline", default="import_feed", help="pipeline name under pipelines/")
    p.add_argument("--step", action="append", choices=sorted(TRANSFORMS),
                   help="run single transforms instead of a pipeline (e.g. --step canonical_sku)")
    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    spec = {"steps": args.step} if args.step else pipeline.load(args.pipeline)
    with open(args.feed, newline="", encoding="utf-8") as f:
        rows = list(csv.DictReader(f))
    w = csv.DictWriter(sys.stdout, fieldnames=list(rows[0]) if rows else ["sku"])
    w.writeheader()
    for r in pipeline.run(spec, rows):
        w.writerow(r)
    return 0
