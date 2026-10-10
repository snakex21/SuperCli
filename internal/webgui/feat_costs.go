package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/account/usagecost"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type costsRow struct {
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	Calls          int           `json:"calls"`
	Input          int64         `json:"input"`
	CachedInput    int64         `json:"cached_input"`
	Output         int64         `json:"output"`
	Reasoning      int64         `json:"reasoning"`
	Total          int64         `json:"total"`
	Cost           statsCostView `json:"cost"`
	LegacyCalls    int           `json:"legacy_calls"`
	MissingFXCalls int           `json:"missing_fx_calls"`
	RateDates      []string      `json:"rate_dates"`
	USDRateDates   []string      `json:"usd_rate_dates,omitempty"`
	RateSources    []string      `json:"rate_sources,omitempty"`
}

func (e *Engine) historyRates() *usagecost.HistoryRates {
	e.costMu.Lock()
	defer e.costMu.Unlock()
	if e.costClosed {
		return nil
	}
	if e.costRates == nil {
		e.costRates = usagecost.NewHistoryRates(e.dataDir)
	}
	return e.costRates
}

type costsView struct {
	Currency        string        `json:"currency"`
	SessionID       string        `json:"session_id"`
	Rows            []costsRow    `json:"rows"`
	Total           statsCostView `json:"total"`
	RatesSource     string        `json:"rates_source"`
	Pending         bool          `json:"pending"`
	RatesGeneration uint64        `json:"rates_generation,omitempty"`
	// Pre-ledger sums retain their frozen cost, without attributing them to
	// the session's last selected model or inventing one provider API call.
	LegacyUnattributed *costsRow `json:"legacy_unattributed,omitempty"`
}

type convertedCost = usagecost.CurrencyAccumulator

func newConvertedCost(tc config.TomlConfig, cache *fx.Cache) *convertedCost {
	return usagecost.NewCurrencyAccumulator(tc, cache)
}

func usageDay(u session.UsageRecord) string {
	return usagecost.UsageDay(u)
}

func (e *Engine) costs(ctx context.Context, sessionID string) (costsView, error) {
	store, err := e.sessionStore()
	if err != nil {
		return costsView{}, err
	}
	if sessionID != "" {
		meta, err := store.Get(sessionID)
		if err != nil {
			return costsView{}, err
		}
		if !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return costsView{}, errSessionOutsideWorkspace
		}
	}
	tc := e.tomlConfig()
	var cache *fx.Cache
	if config.EffectiveCostCurrency(tc) != "USD" {
		if rates := e.historyRates(); rates != nil {
			cache, _ = rates.Cache()
			if cache != nil {
				_ = cache.Refresh(ctx)
			}
		}
	}
	total := newConvertedCost(tc, cache)
	type group struct {
		row  costsRow
		cost *convertedCost
	}
	groups := make(map[[2]string]*group)
	var legacy *group
	err = store.VisitBilling(ctx, sessionID, time.Time{}, func(u session.UsageRecord) {
		total.Add(u)
		if u.Source == "legacy" {
			if legacy == nil {
				legacy = &group{cost: newConvertedCost(tc, cache)}
			}
			legacy.row.Input += u.Input
			legacy.row.Output += u.Output
			legacy.row.CachedInput += u.CachedInput
			legacy.row.Reasoning += u.Reasoning
			legacy.cost.Add(u)
			return
		}
		key := [2]string{u.Provider, u.Model}
		g := groups[key]
		if g == nil {
			g = &group{row: costsRow{Provider: u.Provider, Model: u.Model}, cost: newConvertedCost(tc, cache)}
			groups[key] = g
		}
		g.row.Calls++
		g.row.Input += u.Input
		g.row.CachedInput += u.CachedInput
		g.row.Output += u.Output
		g.row.Reasoning += u.Reasoning
		g.cost.Add(u)
	})
	if err != nil {
		return costsView{}, err
	}
	out := costsView{Currency: config.EffectiveCostCurrency(tc), SessionID: sessionID, Rows: []costsRow{}, Total: total.Summary(), RatesSource: "NBP"}
	if total.MissingFXCalls() > 0 {
		pending, generation := e.historyRates().State()
		out.Pending, out.RatesGeneration = pending, generation
		out.Total.RatesPending, out.Total.RatesGeneration = pending, generation
	}
	for _, g := range groups {
		g.row.Total = g.row.Input + g.row.Output
		g.row.Cost = g.cost.Summary()
		g.row.LegacyCalls, g.row.MissingFXCalls = g.cost.LegacyCalls(), g.cost.MissingFXCalls()
		g.row.RateDates = g.cost.RateDates()
		g.row.USDRateDates, g.row.RateSources = g.cost.USDRateDates(), g.cost.RateSources()
		out.Rows = append(out.Rows, g.row)
	}
	if legacy != nil {
		legacy.row.Total = legacy.row.Input + legacy.row.Output
		legacy.row.Cost = legacy.cost.Summary()
		legacy.row.LegacyCalls, legacy.row.MissingFXCalls = legacy.cost.LegacyCalls(), legacy.cost.MissingFXCalls()
		legacy.row.RateDates = legacy.cost.RateDates()
		legacy.row.USDRateDates, legacy.row.RateSources = legacy.cost.USDRateDates(), legacy.cost.RateSources()
		out.LegacyUnattributed = &legacy.row
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].Provider == out.Rows[j].Provider {
			return out.Rows[i].Model < out.Rows[j].Model
		}
		return out.Rows[i].Provider < out.Rows[j].Provider
	})
	return out, nil
}

// ensureCostRates is invoked only by explicit events, never panel reads.
func (e *Engine) ensureCostRates(ctx context.Context, sessionID string) error {
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	if sessionID != "" {
		meta, err := store.Get(sessionID)
		if err != nil {
			return err
		}
		if !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return errSessionOutsideWorkspace
		}
	}
	days := make(map[string]bool)
	tc := e.tomlConfig()
	if err := store.VisitBilling(ctx, sessionID, time.Time{}, func(u session.UsageRecord) {
		if u.Source == "legacy" {
			return // A session aggregate has no recoverable daily FX rate.
		}
		quote := usagecost.QuoteUsage(tc, u)
		if (quote.State == "manual" || quote.State == "estimated") && quote.Amount > 0 {
			days[usageDay(u)] = true
		}
	}); err != nil {
		return err
	}
	if len(days) == 0 {
		return nil
	}
	ordered := make([]string, 0, len(days))
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Strings(ordered)
	return e.historyRates().EnsureCurrency(ctx, ordered, config.EffectiveCostCurrency(tc))
}

func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := s.eng.costs(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, out)
}

func (s *Server) handleCostRates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.eng.ensureCostRates(ctx, req.SessionID); err != nil {
		http.Error(w, "exchange rates: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// This is a completion notification for work already accepted after real
// usage. It cannot start an exchange fetch or retry one that failed.
func (s *Server) handleCostRatesReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.eng.historyRates().Wait(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
