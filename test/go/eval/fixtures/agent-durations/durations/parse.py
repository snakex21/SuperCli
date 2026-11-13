"""Human duration parsing."""


def parse_duration(text: str) -> int:
    """Parse a human-readable duration into whole seconds.

    Specification:
    - The input is one or more groups of <number><unit>, e.g. "1h30m",
      "2d 4h", "45s". Groups may be separated by single spaces.
    - Units: d = 86400, h = 3600, m = 60, s = 1 seconds. Units are
      case-insensitive ("1H" == "1h").
    - Numbers are non-negative decimal integers without a sign.
    - Each unit may appear at most once, and units must appear in
      descending order (d, then h, then m, then s).
    - Leading and trailing whitespace is ignored.
    - Raise ValueError for: an empty or whitespace-only string, an unknown
      unit, a group without a number, a number without a unit, repeated
      units, units out of order, or more than one space between groups.
    """
    raise NotImplementedError
