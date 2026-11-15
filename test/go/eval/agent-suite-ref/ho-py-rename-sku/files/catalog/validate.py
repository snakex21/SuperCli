"""Startup self-check of the SKU module."""

from . import sku

# Hooks the importer and the search service look up by name at runtime.
REQUIRED_HOOKS = ("canonical_sku", "normalize_sku_prefix", "denormalize_sku")


def missing_hooks(module=sku):
    return [name for name in REQUIRED_HOOKS if not callable(getattr(module, name, None))]
