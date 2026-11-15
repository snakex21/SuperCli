"""Vendor feed importer."""

from .sku import normalize_sku, normalize_skus


def import_rows(rows):
    """Return catalog records keyed by canonical SKU (last row wins)."""
    records = {}
    for row in rows:
        key = normalize_sku(row["sku"])
        records[key] = dict(row, sku=key)
    return records


def known_skus(rows):
    return normalize_skus(row["sku"] for row in rows)
