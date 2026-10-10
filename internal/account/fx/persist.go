package fx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

func (c *Cache) open() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("fx: portable rate directory: %w", err)
	}
	db, err := sql.Open("sqlite", c.path+"?_pragma=busy_timeout(2000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return fmt.Errorf("fx: open portable rate cache: %w", err)
	}
	db.SetMaxOpenConns(1)
	c.db = db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS currency_days (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		usage_day TEXT NOT NULL UNIQUE,
		publication_day TEXT NOT NULL,
		source TEXT NOT NULL,
		multipliers TEXT NOT NULL
	)`)
	if err == nil {
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS currency_quotes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usage_day TEXT NOT NULL,
			currency TEXT NOT NULL,
			multiplier REAL NOT NULL,
			publication_day TEXT NOT NULL,
			usd_publication_day TEXT NOT NULL,
			source TEXT NOT NULL,
			UNIQUE(usage_day,currency)
		)`)
	}
	if err == nil {
		err = c.refreshPersisted(ctx)
	}
	if err != nil {
		db.Close()
		return fmt.Errorf("fx: load portable rate cache: %w", err)
	}
	return nil
}

// refreshPersisted imports new append-only rows from other CLI/GUI instances.
// Caller holds mu (or owns the unpublished constructor). No lookup uses SQL.
func (c *Cache) refreshPersisted(ctx context.Context) error {
	rows, err := c.db.QueryContext(ctx, `SELECT id, usage_day, publication_day, source, multipliers
		FROM currency_days WHERE id > ? ORDER BY id`, c.lastID)
	if err != nil {
		return fmt.Errorf("fx: read historical rates: %w", err)
	}
	defer rows.Close()
	newDays := make(map[string]dayRates)
	lastID := c.lastID
	for rows.Next() {
		var id int64
		var day, raw string
		var rates dayRates
		if err := rows.Scan(&id, &day, &rates.Date, &rates.Source, &raw); err != nil {
			return fmt.Errorf("fx: read historical rate row: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &rates.Multipliers); err != nil {
			return fmt.Errorf("fx: invalid cached multipliers for %s: %w", day, err)
		}
		if err := validateSnapshot(day, rates); err != nil {
			return err
		}
		newDays[day], lastID = rates, id
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("fx: read historical rates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	newQuotes, quoteID, err := c.readPersistedQuotes(ctx)
	if err != nil {
		return err
	}
	c.lookupMu.Lock()
	for day, rates := range newDays {
		if _, exists := c.days[day]; !exists {
			c.days[day] = rates
		}
	}
	for day, quotes := range newQuotes {
		if c.quotes[day] == nil {
			c.quotes[day] = make(map[string]Rate)
		}
		for code, quote := range quotes {
			if _, exists := c.quotes[day][code]; !exists {
				c.quotes[day][code] = quote
			}
		}
	}
	c.lookupMu.Unlock()
	c.lastID = lastID
	c.lastQuoteID = quoteID
	return nil
}

// ValidateSnapshot checks a complete historical day using the cache's rules.
// It does not mutate multipliers or perform database or network operations.
func ValidateSnapshot(day, publicationDay, source string, multipliers map[string]float64) error {
	return validateSnapshot(day, dayRates{Date: publicationDay, Source: source, Multipliers: multipliers})
}

func validateSnapshot(day string, rates dayRates) error {
	date, err := parseDay(day)
	if err != nil {
		return err
	}
	published, err := parseDay(rates.Date)
	if err != nil || published.After(date) || published.Before(date.AddDate(0, 0, -lookbackDays)) || rates.Source != rateSource {
		return fmt.Errorf("fx: invalid cached publication for %s", day)
	}
	for _, currency := range currencyCatalog {
		if currency.legacy && !validMultiplier(rates.Multipliers[currency.code]) {
			return fmt.Errorf("fx: invalid cached %s multiplier for %s", currency.code, day)
		}
	}
	for code, multiplier := range rates.Multipliers {
		if !validCurrencyCode(code) || !validMultiplier(multiplier) || (code == "USD" && multiplier != 1) {
			return fmt.Errorf("fx: invalid cached %s multiplier for %s", code, day)
		}
	}
	return nil
}

// commit preserves the database winner even when another process committed
// after our fetch began. Each complete day's rates are one atomic unique row.
// Caller holds mu. No stale memory snapshot can overwrite a historical row.
func (c *Cache) commit(ctx context.Context, fetched map[string]dayRates) error {
	if len(fetched) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fx: begin historical rate save: %w", err)
	}
	defer tx.Rollback()
	days := make([]string, 0, len(fetched))
	for day := range fetched {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		rates := fetched[day]
		if err := validateSnapshot(day, rates); err != nil {
			return err
		}
		body, err := json.Marshal(rates.Multipliers)
		if err != nil {
			return fmt.Errorf("fx: encode historical rates: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO currency_days
			(usage_day, publication_day, source, multipliers) VALUES (?, ?, ?, ?)`, day, rates.Date, rates.Source, string(body))
		if err != nil {
			return fmt.Errorf("fx: save historical rates: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fx: commit historical rates: %w", err)
	}
	return c.refreshPersisted(ctx)
}
