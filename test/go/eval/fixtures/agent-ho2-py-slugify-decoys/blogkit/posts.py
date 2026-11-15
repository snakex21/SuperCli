"""Post model and permalinks."""

import datetime
from dataclasses import dataclass

from blogkit.text import _slugify_legacy, slugify

LEGACY_CUTOFF = datetime.date(2024, 1, 1)


@dataclass
class Post:
    title: str
    published: datetime.date
    body: str = ""

    @property
    def slug(self):
        if self.published < LEGACY_CUTOFF:
            return _slugify_legacy(self.title)
        return slugify(self.title)

    def permalink(self):
        return "/%04d/%02d/%s/" % (self.published.year, self.published.month, self.slug)
