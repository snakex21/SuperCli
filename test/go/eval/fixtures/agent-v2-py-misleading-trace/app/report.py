"""Builds a cached usage report."""
import os

from cache import reader, writer


def build(stats, cache_dir):
    entry = {
        "user": stats["user"],
        "requests": stats.get("requests", 0),
        "last_error": stats.get("last_error"),
        "plan": stats.get("plan"),
    }
    path = os.path.join(cache_dir, "%s.json" % stats["user"])
    writer.write(path, entry)
    return reader.load(path)
