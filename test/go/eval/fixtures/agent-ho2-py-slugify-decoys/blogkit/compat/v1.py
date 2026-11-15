"""blogkit 1.x API, kept for old plugins. Do not change behaviour."""

import re
import unicodedata


def slugify(title):
    s = unicodedata.normalize("NFKD", title).encode("ascii", "ignore").decode("ascii")
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")
