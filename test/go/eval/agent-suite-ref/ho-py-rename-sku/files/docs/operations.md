# Operations

When an import reports "canonical_sku rewrote N of M SKUs" with N close to M,
the vendor changed its SKU layout; check the feed before publishing.

Single transforms can be run from the CLI:

    python -m catalog.cli feed.csv --step canonical_sku
