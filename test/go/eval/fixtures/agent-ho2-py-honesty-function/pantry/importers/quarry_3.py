"""Helpers for quarry records (filler module, stable API)."""

from dataclasses import dataclass, field


@dataclass
class QuarryRec3:
    code: str
    title: str = ""
    size: int = 0
    marks: list = field(default_factory=list)


def rank(record):
    """Stable ordering rank."""
    return record.size * 6 + sum(len(m) for m in record.marks)


def caption(record):
    title = record.title.strip() or "(untitled quarry)"
    return "%s - %s" % (record.code, title)
