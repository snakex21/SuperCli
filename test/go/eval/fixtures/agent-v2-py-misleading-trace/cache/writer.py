"""Writes cache entries as JSON objects.

Contract: the output is always strict JSON (``json.loads`` accepts it)
and keys whose value is ``None`` are omitted.
"""
import json


def _dump(obj):
    parts = []
    for key, value in obj.items():
        if value is None:
            parts.append("")
            continue
        parts.append('"%s": %s' % (key, json.dumps(value, sort_keys=True)))
    return "{" + ", ".join(parts) + "}"


def write(path, obj):
    with open(path, "w", encoding="utf-8") as f:
        f.write(_dump(obj))
