package webgui

import (
	"context"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

type dailyUsageKey struct {
	session       string
	input, output int64
}

type dailyMainUsage struct {
	created, next int64
}

// Existing TUI calls have no shared identifier across the two databases. Match
// only their main/loop copies, with identical session and token counts, written
// within two seconds in the known sink -> tracker order. A subsequent main call
// closes the matching interval, including when its token counts are identical.
// Delayed/ambiguous or unmatched records remain counted; no history is rewritten.
const dailyUsageMirrorWindow = 2 * time.Second

func dailyTokenTotal(ctx context.Context, store *session.Store, ledger *credits.Storage, since time.Time) int64 {
	var usageTotal int64
	main := map[dailyUsageKey][]*dailyMainUsage{}
	last := map[string]*dailyMainUsage{}
	usageErr := store.VisitUsageSince(ctx, since, func(u session.UsageRecord) {
		// A synthetic pre-ledger total may span weeks. Its last session
		// timestamp does not make those tokens today's measured usage.
		if u.Source == "legacy" {
			return
		}
		usageTotal += u.Input + u.Output
		if u.Source != llm.PurposeMain {
			return
		}
		created := u.CreatedAt.UnixNano()
		if prev := last[u.SessionID]; prev != nil {
			prev.next = created
		}
		candidate := &dailyMainUsage{created: created}
		key := dailyUsageKey{u.SessionID, u.Input, u.Output}
		main[key] = append(main[key], candidate)
		last[u.SessionID] = candidate
	})
	if usageErr != nil {
		usageTotal = 0
		main = nil
	}
	if ledger == nil {
		return usageTotal
	}
	var ledgerTotal, mirrored int64
	ledgerErr := ledger.VisitLedgerSince(ctx, since, func(e credits.LedgerEntry) {
		ledgerTotal += e.Input + e.Output
		if e.Source != credits.SourceLoop || e.ParentSessionID != "" {
			return
		}
		key := dailyUsageKey{e.SessionID, e.Input, e.Output}
		candidates := main[key]
		for len(candidates) > 0 {
			candidate := candidates[0]
			if candidate.created > e.TS {
				break
			}
			candidates = candidates[1:]
			if e.TS-candidate.created > int64(dailyUsageMirrorWindow) ||
				(candidate.next != 0 && e.TS >= candidate.next) {
				continue
			}
			mirrored += e.Input + e.Output
			break
		}
		if main != nil {
			main[key] = candidates
		}
	})
	if ledgerErr != nil {
		return usageTotal
	}
	return usageTotal + ledgerTotal - mirrored
}
