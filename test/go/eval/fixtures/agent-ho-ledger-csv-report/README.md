# ledger export 2026-H1

* `data/transactions/YYYY-MM.csv`: bank export, one file per month, columns
  `txn_id,date,account,memo,currency,amount,status` (standard CSV quoting).
  The bank sometimes re-sends a transaction: the same row appears again later
  in the same file or in a later month's file.
* `data/fx_rates.csv`: monthly conversion rate to EUR per currency.
* `data/accounts.csv`: account master data (region, open/closed).
