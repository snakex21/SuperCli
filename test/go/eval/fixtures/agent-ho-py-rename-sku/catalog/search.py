"""Prefix search over canonical SKUs."""

from catalog import sku


def find(records, query):
    """Exact match on the canonical SKU of query."""
    key = sku.normalize_sku(query)
    return records.get(key)


def find_prefix(records, prefix):
    p = sku.normalize_sku_prefix(prefix)
    return sorted(k for k in records if k.startswith(p))
