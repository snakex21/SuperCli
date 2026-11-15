"""Named record transforms.

Pipelines reference transforms by name (see pipelines/*.json). The name of a
transform is the key in TRANSFORMS; by convention it matches the function.
"""

from . import sku


def strip_whitespace(record):
    return {k: v.strip() if isinstance(v, str) else v for k, v in record.items()}


def lowercase_brand(record):
    out = dict(record)
    if isinstance(out.get("brand"), str):
        out["brand"] = out["brand"].lower()
    return out


def _sku_field(fn):
    def apply(record):
        out = dict(record)
        out["sku"] = fn(out["sku"])
        return out
    apply.__name__ = fn.__name__
    return apply


TRANSFORMS = {
    "strip_whitespace": strip_whitespace,
    "lowercase_brand": lowercase_brand,
    "normalize_sku": _sku_field(sku.normalize_sku),
    "denormalize_sku": _sku_field(sku.denormalize_sku),
}
