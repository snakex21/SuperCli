# Specyfikacja / Specification: transaction summary

Input: `input/transakcje.csv` and `input/kursy.json`. Output: `output/summary.json` and `output/miesiecznie.csv`.

## Input

`input/transakcje.csv` - UTF-8, separator `;`, first line is the header
`id;data;klient;kwota;waluta;status;opis`. Fields may be quoted with `"` (a quoted field may contain `;`,
and `""` inside quotes is a literal quote).

- `id` - transaction id. **The same id can appear more than once; only its first occurrence in the file
  counts** (regardless of status); later rows with that id are duplicates.
- `data` - date `DD.MM.YYYY`.
- `klient` - customer name; trim surrounding whitespace, otherwise keep it exactly (case, Polish letters, commas).
- `kwota` - amount in Polish format: optional leading `-`, thousands separated by a space or a no-break space
  (U+00A0), decimal comma, always two decimals (`1 234,50`, `-50,00`). Negative amounts are refunds.
- `waluta` - `PLN` or `EUR`. EUR amounts are converted to PLN with the rate for the transaction's month from
  `input/kursy.json` (`"YYYY-MM": "rate"`): `PLN grosze = round_half_up(EUR cents x rate)` to a whole grosz.
- `status` - only `ZAKSIĘGOWANA` rows are counted. `ANULOWANA` and `OCZEKUJĄCA` rows are skipped.

A row is *counted* when it is the first occurrence of its id and its status is `ZAKSIĘGOWANA`.

## output/summary.json

```json
{
  "customers": [
    {"klient": "<name>", "transakcje": <counted rows>, "suma_gr": <sum of counted amounts in grosze, refunds included>,
     "zwroty_gr": <sum of the absolute values of counted negative amounts, in grosze>}
  ],
  "razem_gr": <sum of all counted amounts in grosze>,
  "pominiete": {"anulowane": <n>, "oczekujace": <n>, "duplikaty": <n>}
}
```

- `customers`: one entry per customer with at least one counted row, sorted by `suma_gr` descending, then by
  `klient` ascending (plain Unicode code point order).
- `pominiete` counts skipped rows: duplicates (any status) are counted only as `duplikaty`; first occurrences
  are counted as `anulowane` / `oczekujace` by status.
- All amounts are integers (grosze). Key order and whitespace in the JSON file do not matter.

## output/miesiecznie.csv

Comma-separated, header `miesiac,klient,suma_pln`, one row per (month, customer) with at least one counted row:

- `miesiac` - `YYYY-MM` of the transaction date;
- `klient` - as above (quote it per RFC 4180 when it contains a comma or a quote);
- `suma_pln` - sum of the counted amounts in PLN with a dot and exactly two decimals (`1234.50`, `-50.00`), no
  thousands separator.

Rows sorted by `miesiac`, then `klient` (Unicode code point order).
