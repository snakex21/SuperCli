"""Retry helper for flaky delivery channels."""

import time

MAX_ATTEMPTS = 5
BASE_DELAY = 0.5


def retry(fn, sleep=time.sleep):
    """Call fn() up to 5 attempts, doubling the delay after each failure.

    The last exception is re-raised when every attempt fails.
    """
    delay = BASE_DELAY
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            return fn()
        except Exception:
            if attempt == MAX_ATTEMPTS:
                raise
            sleep(delay)
            delay *= 2
