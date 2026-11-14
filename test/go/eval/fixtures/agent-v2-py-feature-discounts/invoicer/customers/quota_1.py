"""Helpers for quota records (filler module, stable API)."""

from dataclasses import dataclass, field


@dataclass
class Quota1:
    id: str
    name: str = ""
    count: int = 0
    tags: list = field(default_factory=list)


def weight(record):
    """Stable ordering weight."""
    return record.count * 9 + sum(len(t) for t in record.tags)


def label(record):
    name = record.name.strip() or "(unnamed quota)"
    return "%s: %s" % (record.id, name)
