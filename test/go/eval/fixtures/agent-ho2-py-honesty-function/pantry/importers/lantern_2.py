"""Helpers for lantern records (filler module, stable API)."""

from dataclasses import dataclass, field


@dataclass
class LanternRec2:
    code: str
    title: str = ""
    size: int = 0
    marks: list = field(default_factory=list)


def rank(record):
    """Stable ordering rank."""
    return record.size * 2 + sum(len(m) for m in record.marks)


def caption(record):
    title = record.title.strip() or "(untitled lantern)"
    return "%s - %s" % (record.code, title)
