package webgui

import (
	"context"
	"fmt"

	"supercli/internal/account/credits"
)

// creditStorage owns only the connection and schema setup, not a cached total.
// Each query observes ledger writes made by another GUI or TUI process.
func (e *Engine) creditStorage(ctx context.Context) (*credits.Storage, error) {
	e.creditMu.Lock()
	defer e.creditMu.Unlock()
	if e.creditClosed {
		return nil, fmt.Errorf("webgui engine is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.creditLedger != nil {
		return e.creditLedger, nil
	}
	db, err := openDataDB(e.dataDir)
	if err != nil {
		return nil, err
	}
	ledger := credits.NewStorage(db)
	if err := ledger.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	e.creditDB = db
	e.creditLedger = ledger
	return ledger, nil
}
