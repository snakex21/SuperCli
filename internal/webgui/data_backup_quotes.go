package webgui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"supercli/internal/account/fx"
)

// Older backups have only the original immutable day snapshots. Newer backups
// may additionally contain append-only quotes for other official currencies.
func validateCurrencyQuoteBackup(ctx context.Context, db *sql.DB) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='currency_quotes')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var valid bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sqlite_schema WHERE name='currency_quotes' AND type='table')
		AND EXISTS(SELECT 1 FROM pragma_table_info('currency_quotes') WHERE name='id' AND upper(type)='INTEGER' AND pk=1)
		AND EXISTS(SELECT 1 FROM pragma_index_list('currency_quotes') l WHERE l."unique"=1 AND l.partial=0
			AND (SELECT COUNT(*) FROM pragma_index_info(l.name))=2
			AND (SELECT name FROM pragma_index_info(l.name) WHERE seqno=0)='usage_day'
			AND (SELECT name FROM pragma_index_info(l.name) WHERE seqno=1)='currency')`).Scan(&valid); err != nil {
		return fmt.Errorf("invalid currency quote schema: %w", err)
	}
	if !valid {
		return errors.New("currency quote schema requires a table, ID primary key and unique day/currency")
	}
	rows, err := db.QueryContext(ctx, `SELECT q.id,q.usage_day,q.currency,q.multiplier,q.publication_day,q.usd_publication_day,q.source,d.publication_day
		FROM currency_quotes q LEFT JOIN currency_days d ON d.usage_day=q.usage_day ORDER BY q.id`)
	if err != nil {
		return fmt.Errorf("invalid currency quote schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var day, currency, publication, usdPublication, source string
		var multiplier float64
		var anchor sql.NullString
		if err := rows.Scan(&id, &day, &currency, &multiplier, &publication, &usdPublication, &source, &anchor); err != nil {
			return err
		}
		if id <= 0 || !anchor.Valid || anchor.String != usdPublication {
			return errors.New("currency quote snapshot has an invalid ID or frozen USD reference")
		}
		if err := fx.ValidateQuote(day, currency, publication, usdPublication, source, multiplier); err != nil {
			return err
		}
	}
	return rows.Err()
}
