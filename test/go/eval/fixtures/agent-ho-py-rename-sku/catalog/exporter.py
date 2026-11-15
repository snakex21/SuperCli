"""Vendor feed exporter (uses the vendor SKU layout)."""

from .sku import denormalize_sku


def export_rows(records, sep="_"):
    return [dict(r, sku=denormalize_sku(r["sku"], sep)) for r in sorted(records.values(), key=lambda r: r["sku"])]
