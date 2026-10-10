package usagecost

import (
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// CallIdentity uses the actual backend captured by the factory. The fallback
// keeps callers with older/injected Metered providers compatible. No API key,
// URL path, prompt or connection digest is copied into the persisted record.
func CallIdentity(tc config.TomlConfig, stat llm.CallStat, fallback session.UsageRecord) session.UsageRecord {
	u := fallback
	if stat.ProviderType != "" {
		u.Provider, u.ProviderType, u.EndpointHost = stat.ProviderType, stat.ProviderType, stat.EndpointHost
		// Two named profiles may share endpoint/key/model but have different
		// configured prices. Preserve the captured selected profile when it is
		// compatible with the actual connection.
		for _, p := range tc.Providers {
			if p.Name == fallback.Provider && llm.ProviderConnectionKey(p.Type, p.BaseURL, p.APIKey) == stat.ConnectionKey {
				u.Provider = p.Name
				goto identified
			}
		}
		for _, p := range tc.Providers {
			if llm.ProviderConnectionKey(p.Type, p.BaseURL, p.APIKey) == stat.ConnectionKey {
				u.Provider = p.Name
				if p.Model == stat.Model {
					break
				}
			}
		}
	}
identified:
	if stat.Model != "" {
		u.Model = stat.Model
	}
	u.CreatedAt = stat.StartedAt
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	return u
}

// CallUsage projects provider counters without counting cache and reasoning a
// second time: they are subsets of input and output respectively.
func CallUsage(tc config.TomlConfig, stat llm.CallStat, fallback session.UsageRecord) session.UsageRecord {
	u := CallIdentity(tc, stat, fallback)
	u.Input, u.Output = int64(stat.TokensIn), int64(stat.TokensOut)
	u.CachedInput, u.Reasoning = int64(stat.TokensCached), int64(stat.TokensReasoning)
	u.HasCachedInput, u.HasReasoning = stat.TokensCached > 0, stat.TokensReasoning > 0
	u.TTFTMS = stat.TTFT.Milliseconds()
	u.DurationMS, u.HasTiming = CallTiming(stat)
	u.PrefillEvaluated, u.PrefillTokensPerSecond = int64(stat.PrefillEvaluated), stat.PrefillTokensPerSecond
	u.PrefillBudget, u.PrefillBudgetSource = stat.PrefillBudget, stat.PrefillBudgetSource
	u.ContextSystem, u.ContextUser, u.ContextAssistant = stat.Request.System, stat.Request.User, stat.Request.Assistant
	u.ContextTool, u.ContextOther = stat.Request.Tool, stat.Request.Other
	u.Source = stat.Purpose
	price := FreezeQuote(tc, u, false)
	u.PriceSnapshot = &price
	return u
}

// Failed/canceled streams may expose incomplete or final accounting after
// cancellation. Keep their tokens, but do not invent a completed speed sample.
func CallTiming(stat llm.CallStat) (int64, bool) {
	if stat.Failed || stat.Canceled || stat.Duration.Milliseconds() <= 0 || stat.TTFT < 0 || stat.TTFT > stat.Duration {
		return 0, false
	}
	return stat.Duration.Milliseconds(), true
}
