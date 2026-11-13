import argparse

from . import config


def build_parser():
    p = argparse.ArgumentParser(prog="servecfg")
    p.add_argument("--host", default=config.DEFAULT_HOST, help="interface to bind")
    p.add_argument("--port", type=int, default=config.DEFAULT_PORT, help="TCP port")
    p.add_argument("--workers", type=int, default=config.DEFAULT_WORKERS, help="worker processes")
    p.add_argument("--debug", action="store_true", help="enable debug logging")
    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    token = config.load_token()
    print(f"serving on {args.host}:{args.port} with {args.workers} workers (auth={'on' if token else 'off'})")


if __name__ == "__main__":
    main()
