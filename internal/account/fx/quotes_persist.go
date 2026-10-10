package fx

import (
	"context"
	"fmt"
	"sort"
)

func (c *Cache) readPersistedQuotes(ctx context.Context) (map[string]map[string]Rate, int64, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT q.id,q.usage_day,q.currency,q.multiplier,q.publication_day,q.usd_publication_day,q.source,COALESCE(d.publication_day,'')
		FROM currency_quotes q LEFT JOIN currency_days d ON d.usage_day=q.usage_day WHERE q.id>? ORDER BY q.id`, c.lastQuoteID)
	if err != nil {
		return nil, 0, fmt.Errorf("fx: read additional historical quotes: %w", err)
	}
	defer rows.Close()
	quotes := make(map[string]map[string]Rate)
	lastID := c.lastQuoteID
	for rows.Next() {
		var id int64
		var day, currency, anchor string
		var rate Rate
		if err := rows.Scan(&id, &day, &currency, &rate.Multiplier, &rate.Date, &rate.USDDate, &rate.Source, &anchor); err != nil {
			return nil, 0, err
		}
		if err := ValidateQuote(day, currency, rate.Date, rate.USDDate, rate.Source, rate.Multiplier); err != nil {
			return nil, 0, err
		}
		if anchor != rate.USDDate {
			return nil, 0, fmt.Errorf("fx: quote does not match its frozen USD snapshot")
		}
		if quotes[day] == nil {
			quotes[day] = make(map[string]Rate)
		}
		quotes[day][currency], lastID = rate, id
	}
	return quotes, lastID, rows.Err()
}

func (c *Cache) commitQuotes(ctx context.Context, fetched map[string]map[string]Rate) error {
	if len(fetched) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var days []string
	for day := range fetched {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		var codes []string
		for code := range fetched[day] {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			rate := fetched[day][code]
			if err := ValidateQuote(day, code, rate.Date, rate.USDDate, rate.Source, rate.Multiplier); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO currency_quotes
				(usage_day,currency,multiplier,publication_day,usd_publication_day,source) VALUES(?,?,?,?,?,?)`,
				day, code, rate.Multiplier, rate.Date, rate.USDDate, rate.Source); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return c.refreshPersisted(ctx)
}
