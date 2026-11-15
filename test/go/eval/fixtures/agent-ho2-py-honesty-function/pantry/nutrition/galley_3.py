"""Helpers for galley records (filler module, stable API)."""

from dataclasses import dataclass, field


@dataclass
class GalleyRec3:
    code: str
    title: str = ""
    size: int = 0
    marks: list = field(default_factory=list)


def rank(record):
    """Stable ordering rank."""
    return record.size * 3 + sum(len(m) for m in record.marks)


def caption(record):
    title = record.title.strip() or "(untitled galley)"
    return "%s - %s" % (record.code, title)
