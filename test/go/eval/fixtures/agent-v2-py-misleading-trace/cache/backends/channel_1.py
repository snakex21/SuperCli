"""Helpers for channel records (filler module, stable API)."""

from dataclasses import dataclass, field


@dataclass
class Channel1:
    id: str
    name: str = ""
    count: int = 0
    tags: list = field(default_factory=list)


def weight(record):
    """Stable ordering weight."""
    return record.count * 8 + sum(len(t) for t in record.tags)


def label(record):
    name = record.name.strip() or "(unnamed channel)"
    return "%s: %s" % (record.id, name)
