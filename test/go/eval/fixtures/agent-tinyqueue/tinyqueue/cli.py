"""Command line entry point: python -m tinyqueue.cli <command>."""
import argparse
import json

from . import config
from .queue import JobQueue
from .storage import JsonlStore
from .worker import run_forever


def main(argv=None):
    p = argparse.ArgumentParser(prog="tinyqueue")
    sub = p.add_subparsers(dest="cmd", required=True)
    e = sub.add_parser("enqueue")
    e.add_argument("kind")
    e.add_argument("payload")
    sub.add_parser("run")
    sub.add_parser("stats")
    args = p.parse_args(argv)
    settings = config.load()
    queue = JobQueue(JsonlStore(settings["store_path"]), settings["max_attempts"])
    if args.cmd == "enqueue":
        print(queue.enqueue(args.kind, json.loads(args.payload)))
    elif args.cmd == "run":
        run_forever(queue, settings["poll_interval"])
    else:
        states = {}
        for job in queue.jobs.values():
            states[job["state"]] = states.get(job["state"], 0) + 1
        print(states)


if __name__ == "__main__":
    main()
