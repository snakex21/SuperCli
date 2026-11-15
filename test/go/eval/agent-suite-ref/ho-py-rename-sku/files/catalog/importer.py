"""Vendor feed importer."""

from .sku import canonical_sku, normalize_skus


def import_rows(rows):
    """Return catalog records keyed by canonical SKU (last row wins)."""
    records = {}
    for row in rows:
        key = canonical_sku(row["sku"])
        records[key] = dict(row, sku=key)
    return records


def known_skus(rows):
    return normalize_skus(row["sku"] for row in rows)
