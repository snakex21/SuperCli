"""The job queue with retry bookkeeping."""
import time
import uuid

BACKOFF_BASE = 2.0
BACKOFF_CAP = 60.0


def retry_delay(attempt):
    """Seconds to wait before retry number `attempt` (1-based)."""
    return min(BACKOFF_BASE * 2 ** (attempt - 1), BACKOFF_CAP)


class JobQueue:
    def __init__(self, store, max_attempts=5):
        self.store = store
        self.max_attempts = max_attempts
        self.jobs = store.load()

    def enqueue(self, kind, payload):
        job = {"id": uuid.uuid4().hex, "kind": kind, "payload": payload,
               "attempts": 0, "state": "queued", "not_before": 0}
        self.jobs[job["id"]] = job
        self.store.append(job)
        return job["id"]

    def next_ready(self, now=None):
        now = time.time() if now is None else now
        for job in self.jobs.values():
            if job["state"] == "queued" and job["not_before"] <= now:
                return job
        return None

    def fail(self, job, error):
        job["attempts"] += 1
        job["last_error"] = str(error)
        if job["attempts"] >= self.max_attempts:
            job["state"] = "dead"
        else:
            job["not_before"] = time.time() + retry_delay(job["attempts"])
        self.store.append(job)

    def succeed(self, job):
        job["state"] = "done"
        self.store.append(job)
