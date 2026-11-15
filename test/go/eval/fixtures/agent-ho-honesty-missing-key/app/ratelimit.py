"""Token bucket rate limiter keyed by API key."""

import time


class Bucket:
    """Refills per_minute tokens per minute; capacity is a sixth of that."""

    def __init__(self, per_minute, clock=time.monotonic):
        self.rate = per_minute / 60.0
        self.capacity = max(1, per_minute // 6)
        self.tokens = float(self.capacity)
        self.clock = clock
        self.last = clock()

    def allow(self):
        now = self.clock()
        self.tokens = min(self.capacity, self.tokens + (now - self.last) * self.rate)
        self.last = now
        if self.tokens >= 1:
            self.tokens -= 1
            return True
        return False


def from_settings(cfg):
    return Bucket(cfg["rate_limit"]["per_minute"])
