package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/usagecost"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// Captures the destination when the run starts: a late usage frame cannot be
// attributed to a subsequently opened conversation.
func (m Model) sessionUsageSink() llm.CallSink {
	store, id, provider := m.sessionStore, m.sessionID, m.activeProviderName()
	if store == nil || id == "" {
		return nil
	}
	tc, _ := config.ResolveConfig(m.dataDir, m.home, "")
	fallback := session.UsageRecord{SessionID: id, Provider: provider}
	if m.llm != nil {
		fallback.Model = m.llm.Name()
	}
	for _, p := range tc.Providers {
		if p.Name == provider {
			fallback.ProviderType = p.Type
			break
		}
	}
	rates := m.usageRates
	return func(s llm.CallStat) {
		if s.TokensIn == 0 && s.TokensOut == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		u := usagecost.CallUsage(tc, s, fallback)
		if err := store.AppendUsage(ctx, u); err == nil && config.EffectiveCostCurrency(tc) != "USD" && u.PriceSnapshot.AmountUSD != nil && *u.PriceSnapshot.AmountUSD > 0 {
			rates.WarmCurrency(config.EffectiveCostCurrency(tc), u.PriceSnapshot.UsageDay)
		}
	}
}

// This command only runs after a currency preference changes. Rendering and
// opening statistics never trigger exchange requests.
func (m Model) refreshUsageRates(tc config.TomlConfig) tea.Cmd {
	store, rates := m.sessionStore, m.usageRates
	if store == nil || rates == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		days := map[string]bool{}
		_ = store.VisitBilling(ctx, "", time.Time{}, func(u session.UsageRecord) {
			if u.Source != "legacy" && u.PriceSnapshot != nil && u.PriceSnapshot.AmountUSD != nil && *u.PriceSnapshot.AmountUSD > 0 {
				days[u.PriceSnapshot.UsageDay] = true
			}
		})
		list := make([]string, 0, len(days))
		for day := range days {
			list = append(list, day)
		}
		_ = rates.EnsureCurrency(ctx, list, config.EffectiveCostCurrency(tc))
		return StatusRefreshMsgValue()
	}
}
