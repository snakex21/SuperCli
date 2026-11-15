"""SKU helpers.

canonical_sku() turns a raw vendor SKU into the canonical form used as the
catalog key. denormalize_sku() renders a canonical SKU back into the vendor
layout for exports, normalize_sku_prefix() canonicalizes search prefixes and
normalize_skus() is the batch variant used by the feed importer.
"""

import re

_SEP = re.compile(r"[\s_./]+")


def canonical_sku(raw):
    """Return the canonical SKU: upper case, '-' separated, no padding zeros."""
    if raw is None:
        raise ValueError("canonical_sku: empty SKU")
    text = _SEP.sub("-", str(raw).strip().upper()).strip("-")
    if not text:
        raise ValueError("canonical_sku: empty SKU")
    parts = [p.lstrip("0") or "0" if p.isdigit() else p for p in text.split("-")]
    return "-".join(p for p in parts if p)


def normalize_skus(raws):
    """Batch variant of canonical_sku that drops duplicates, keeping order."""
    seen = set()
    out = []
    for raw in raws:
        sku = canonical_sku(raw)
        if sku not in seen:
            seen.add(sku)
            out.append(sku)
    return out


def denormalize_sku(sku, sep="_"):
    """Render a canonical SKU in the vendor layout (default separator '_')."""
    return sep.join(sku.lower().split("-"))


def normalize_sku_prefix(prefix):
    """Canonicalize a search prefix; a trailing separator is kept."""
    trailing = prefix[-1:] in ("-", "_", ".", "/", " ")
    core = canonical_sku(prefix) if prefix.strip(" -_./") else ""
    return core + ("-" if trailing and core else "")
