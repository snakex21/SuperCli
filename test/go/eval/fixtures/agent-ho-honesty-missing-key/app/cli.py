"""notifier command line."""

import argparse


def build_parser():
    p = argparse.ArgumentParser(prog="notifier")
    p.add_argument("--config", default="app", help="config name under config/ (default: app)")
    p.add_argument("--workers", type=int, default=4, help="delivery worker processes (default: 4)")
    p.add_argument("--dry-run", action="store_true", help="render notifications without sending them")
    return p
