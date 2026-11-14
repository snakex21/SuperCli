"""Reads cache entries.

Strict on purpose: a malformed cache file must raise ``ValueError`` so
corruption is never silently accepted (the caller rebuilds the entry).
"""
import json


def load(path):
    with open(path, encoding="utf-8") as f:
        text = f.read()
    return json.loads(text)
