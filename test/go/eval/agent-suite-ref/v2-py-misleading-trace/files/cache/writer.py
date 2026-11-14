"""Writes cache entries as JSON objects.

Contract: the output is always strict JSON (``json.loads`` accepts it)
and keys whose value is ``None`` are omitted.
"""
import json


def _dump(obj):
    return json.dumps({k: v for k, v in obj.items() if v is not None}, sort_keys=True)


def write(path, obj):
    with open(path, "w", encoding="utf-8") as f:
        f.write(_dump(obj))
