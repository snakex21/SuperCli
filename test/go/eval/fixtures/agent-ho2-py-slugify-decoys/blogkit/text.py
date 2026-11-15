"""Text helpers shared by the blog engine."""

import re
import unicodedata


def _ascii_fold(s):
    """Strip accents: NFKD-decompose and drop everything that is not ASCII."""
    return unicodedata.normalize("NFKD", s).encode("ascii", "ignore").decode("ascii")


def slugify(title, max_len=60):
    """URL slug for a post title: "Hello, World!" -> "hello-world"."""
    s = _ascii_fold(title).lower()
    s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
    return s[:max_len].rstrip("-")


def slugify_filename(name):
    """Safe attachment file name: keeps the extension, uses underscores."""
    base, dot, ext = name.rpartition(".")
    if not dot:
        base, ext = name, ""
    base = re.sub(r"[^A-Za-z0-9]+", "_", _ascii_fold(base)).strip("_") or "file"
    return base + ("." + _ascii_fold(ext).lower() if ext else "")


def _slugify_legacy(title):
    """Permalink slug of posts published before 2024.

    Old URLs are linked from all over the web, so this must keep producing
    exactly the same output.
    """
    s = _ascii_fold(title).lower()
    return re.sub(r"\s+", "-", s.strip())


def excerpt(body, words=30):
    """First `words` words of body, with an ellipsis when cut."""
    parts = body.split()
    return " ".join(parts[:words]) + (" ..." if len(parts) > words else "")
