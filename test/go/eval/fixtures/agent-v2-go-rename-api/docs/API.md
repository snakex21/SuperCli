# shopcore API

## money

- `money.ParseAmount(s string) (Amount, error)`: loose parser, accepts
  `1,234.5`, `12`, `-0.5`.
- `money.ParseAmountStrict(s string) (Amount, error)`: exactly two
  decimals, no separators.
- `Amount.String()`: `-12.34` style rendering.

Error messages (`money.ParseAmount: empty input`, ...) are matched by
operator scripts and must not change.

## catalog

- `(*Importer).ParseAmount(field)`: supplier feeds with a currency prefix.
