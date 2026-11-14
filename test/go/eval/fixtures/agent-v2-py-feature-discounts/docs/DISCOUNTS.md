# Discount codes (spec)

1. `invoicer.models.Discount` is a dataclass with fields `code: str`,
   `percent: int | None = None`, `amount_cents: int | None = None`,
   `min_subtotal_cents: int = 0`. Exactly one of `percent` and
   `amount_cents` must be set, otherwise `__post_init__` raises
   `ValueError`. `percent` must be between 1 and 100 (inclusive),
   `amount_cents` must be positive; otherwise `ValueError`.
2. `Invoice` gets a new last field `discount_code: str | None = None`.
3. `invoicer.pricing.discount_cents(inv, discounts)` returns the discount
   in cents for an invoice, where `discounts` maps codes to `Discount`:
   - no `discount_code` on the invoice -> 0;
   - codes are case-insensitive (`spring10` and `SPRING10` are the same
     code); an unknown code raises `KeyError`;
   - if the subtotal is below `min_subtotal_cents` -> 0;
   - percent discounts are `round_half_up(subtotal * percent, 100)`;
   - amount discounts are capped at the subtotal.
4. `invoicer.pricing.total_cents(inv, discounts=None)`: the discount is
   subtracted from the subtotal **before** tax; tax is computed on the
   discounted amount. `discounts=None` means no known codes.
5. Storage: the invoice JSON has an optional `"discount_code"` key
   (omitted when there is no code; files without it still load).
   `invoicer.storage.load_discounts(path)` reads a JSON list of objects
   (`code`, `percent` or `amount_cents`, optional `min_subtotal_cents`)
   and returns a dict mapping each code to its `Discount`.
6. CLI: `total` accepts `--discounts PATH`. When the discount is
   non-zero, a line `Discount (CODE): -X.XX` (CODE upper-cased) is
   printed between `Subtotal` and `Tax`, and Tax/Total use the
   discounted amount. Output is unchanged for invoices without a code.
7. Add tests in `tests/test_discounts.py` (at least 4 test methods).
