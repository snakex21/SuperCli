# catalog

Ingests vendor product feeds into the catalog.

* `catalog.sku.normalize_sku(raw)` is the one function that defines what a
  catalog key looks like. Never build keys by hand.
* `denormalize_sku` and `normalize_sku_prefix` are the export and search
  counterparts; `normalize_skus` is the batch helper.
* Pipelines in `pipelines/` list transforms by name, e.g. `"normalize_sku"`.

Run the tests with `python -m unittest discover -s tests -t .`.
