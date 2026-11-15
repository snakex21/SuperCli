"""Attachment storage paths."""

import os

from blogkit.text import slugify_filename


def attachment_path(post_slug, filename):
    return os.path.join("media", post_slug, slugify_filename(filename)).replace(os.sep, "/")
