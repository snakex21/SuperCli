"""Polling worker."""
import time

HANDLERS = {}


def handler(kind):
    def register(fn):
        HANDLERS[kind] = fn
        return fn
    return register


@handler("echo")
def _echo(payload):
    print(payload)


def run_forever(queue, poll_interval=0.5):
    while True:
        job = queue.next_ready()
        if job is None:
            time.sleep(poll_interval)
            continue
        try:
            HANDLERS[job["kind"]](job["payload"])
        except Exception as exc:  # noqa: BLE001
            queue.fail(job, exc)
        else:
            queue.succeed(job)
