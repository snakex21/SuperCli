"""Human duration parsing."""
import re

_UNITS = {"d": 86400, "h": 3600, "m": 60, "s": 1}
_ORDER = "dhms"
_GROUP = re.compile(r"(\d+)([a-z])")


def parse_duration(text: str) -> int:
    s = text.strip().lower()
    if not s:
        raise ValueError("empty duration")
    pos = 0
    total = 0
    last = -1
    while pos < len(s):
        if pos > 0 and s[pos] == " ":
            pos += 1
        m = _GROUP.match(s, pos)
        if not m or m.group(2) not in _UNITS:
            raise ValueError(f"bad duration: {text!r}")
        idx = _ORDER.index(m.group(2))
        if idx <= last:
            raise ValueError(f"unit order: {text!r}")
        last = idx
        total += int(m.group(1)) * _UNITS[m.group(2)]
        pos = m.end()
    return total
