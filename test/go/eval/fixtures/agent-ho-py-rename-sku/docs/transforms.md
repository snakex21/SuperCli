# Transforms

| name | what it does |
|---|---|
| `strip_whitespace` | trims every string field |
| `lowercase_brand` | lower-cases `brand` |
| `normalize_sku` | canonical SKU via `catalog.sku.normalize_sku` |
| `denormalize_sku` | vendor layout via `catalog.sku.denormalize_sku` |

A pipeline that exports must not run `normalize_sku` after
`denormalize_sku`. The search service calls `normalize_sku_prefix` directly.
