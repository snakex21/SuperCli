package webgui

import (
	"context"
	"supercli/internal/account/fx"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type statsUsageRead struct {
	Records         int // includes preserved pre-ledger aggregates
	Calls           int
	Tokens          statsTokensView
	Last            session.UsageRecord
	Main            session.UsageRecord
	HasMain         bool
	Cost            statsCostView
	GenerationSpeed statsGenerationSpeedView
}

func readStatsUsage(ctx context.Context, store *session.Store, sessionID string, tc config.TomlConfig, caches ...*fx.Cache) (statsUsageRead, error) {
	var out statsUsageRead
	var cache *fx.Cache
	if len(caches) > 0 {
		cache = caches[0]
	}
	cost := newConvertedCost(tc, cache)
	err := store.VisitUsageForStats(ctx, sessionID, func(u session.UsageRecord) {
		out.Records++
		out.GenerationSpeed.add(u)
		if u.Source != "legacy" {
			out.Calls++
			out.Last = u
			if isMainUsageSource(u.Source) {
				out.Main = u
				out.HasMain = true
			}
		}
		out.Tokens.Input += u.Input
		out.Tokens.Output += u.Output
		out.Tokens.CachedInput += u.CachedInput
		out.Tokens.Reasoning += u.Reasoning
		out.Tokens.HasCached = out.Tokens.HasCached || u.HasCachedInput
		out.Tokens.HasReasoning = out.Tokens.HasReasoning || u.HasReasoning
		cost.Add(u)
	})
	if err != nil {
		return statsUsageRead{}, err
	}
	out.Cost = cost.Summary()
	out.GenerationSpeed.finish()
	return out, nil
}

func isMainUsageSource(source string) bool {
	switch source {
	case "", "model", "main":
		return true
	default:
		return false
	}
}
