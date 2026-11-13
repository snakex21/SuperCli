"""Append-only job persistence."""
import json


class JsonlStore:
    """Persists every job state change as one JSON object per line."""

    def __init__(self, path):
        self.path = path

    def append(self, job):
        with open(self.path, "a", encoding="utf-8") as f:
            f.write(json.dumps(job) + "\n")

    def load(self):
        jobs = {}
        try:
            with open(self.path, encoding="utf-8") as f:
                for line in f:
                    job = json.loads(line)
                    jobs[job["id"]] = job
        except FileNotFoundError:
            pass
        return jobs
