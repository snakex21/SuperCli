package webgui

import (
	"context"
	"net/http"
	"sort"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

const (
	statsHistoryDefaultDays = 30
	statsHistoryMaxDays     = 90
)

// statsHistoryTokens splits input into evaluated (input-cached) and cached
// so the stacked bar never double counts the cached subset.
type statsHistoryTokens struct {
	Evaluated int64 `json:"evaluated"`
	Cached    int64 `json:"cached"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
}

func (t *statsHistoryTokens) add(u session.UsageRecord) {
	cached := u.CachedInput
	if cached < 0 {
		cached = 0
	}
	if cached > u.Input {
		cached = u.Input
	}
	t.Evaluated += u.Input - cached
	t.Cached += cached
	t.Output += u.Output
	t.Reasoning += u.Reasoning
}

type statsHistoryModel struct {
	Provider string             `json:"provider"`
	Model    string             `json:"model"`
	Calls    int                `json:"calls"`
	Tokens   statsHistoryTokens `json:"tokens"`
}

type statsHistoryDay struct {
	Day         string              `json:"day"`
	Calls       int                 `json:"calls"`
	Tokens      statsHistoryTokens  `json:"tokens"`
	Models      []statsHistoryModel `json:"models"`
	Cost        statsCostView       `json:"cost"`
	AvgTTFTMS   int64               `json:"avg_ttft_ms"`
	TTFTCalls   int                 `json:"ttft_calls"`
	ModelTimeMS int64               `json:"model_time_ms"`
	ToolTimeMS  int64               `json:"tool_time_ms"`
}

type statsHistoryTool struct {
	Name   string `json:"name"`
	TimeMS int64  `json:"time_ms"`
}

type statsHistoryView struct {
	Days     int                `json:"days"`
	From     string             `json:"from"`
	To       string             `json:"to"`
	Timezone string             `json:"timezone"`
	Currency string             `json:"currency"`
	Series   []statsHistoryDay  `json:"series"`
	Models   []string           `json:"models"`
	Tools    []statsHistoryTool `json:"tools"`
	Total    statsCostView      `json:"total_cost"`
	// ToolCalls are not persisted per tool without a schema change, so
	// the tool series carries only wall time.
	ToolCallsAvailable bool `json:"tool_calls_available"`
}

func clampHistoryDays(days int) int {
	if days <= 0 {
		return statsHistoryDefaultDays
	}
	if days > statsHistoryMaxDays {
		return statsHistoryMaxDays
	}
	return days
}

// statsHistory aggregates usage across all sessions per local calendar day.
// Two streaming, index-bounded queries; no per-day round trips.
func (e *Engine) statsHistory(ctx context.Context, days int, now time.Time, loc *time.Location) (statsHistoryView, error) {
	days = clampHistoryDays(days)
	if loc == nil {
		loc = time.Local
	}
	store, err := e.sessionStore()
	if err != nil {
		return statsHistoryView{}, err
	}
	tc := e.tomlConfig()
	var cache *fx.Cache
	if config.EffectiveCostCurrency(tc) != "USD" {
		if rates := e.historyRates(); rates != nil {
			cache, _ = rates.Cache()
		}
	}
	start := session.HistoryWindowStart(now, days, loc)
	keys := session.HistoryDayKeys(start, days)
	out := statsHistoryView{
		Days: days, From: keys[0], To: keys[len(keys)-1],
		Timezone: loc.String(), Currency: config.EffectiveCostCurrency(tc),
		Series: make([]statsHistoryDay, days), Models: []string{}, Tools: []statsHistoryTool{},
	}
	index := make(map[string]int, days)
	costs := make([]*convertedCost, days)
	models := make([]map[[2]string]*statsHistoryModel, days)
	ttftSum := make([]int64, days)
	modelNames := map[string]bool{}
	total := newConvertedCost(tc, cache)
	for i, k := range keys {
		out.Series[i].Day = k
		index[k] = i
	}
	err = store.VisitUsageHistory(ctx, start, func(u session.UsageRecord) {
		i, ok := index[u.CreatedAt.In(loc).Format("2006-01-02")]
		if !ok {
			return
		}
		d := &out.Series[i]
		d.Calls++
		d.Tokens.add(u)
		if costs[i] == nil {
			costs[i] = newConvertedCost(tc, cache)
		}
		costs[i].Add(u)
		total.Add(u)
		if u.HasTiming {
			d.ModelTimeMS += u.DurationMS
			if u.TTFTMS > 0 {
				ttftSum[i] += u.TTFTMS
				d.TTFTCalls++
			}
		}
		if models[i] == nil {
			models[i] = map[[2]string]*statsHistoryModel{}
		}
		key := [2]string{u.Provider, u.Model}
		m := models[i][key]
		if m == nil {
			m = &statsHistoryModel{Provider: u.Provider, Model: u.Model}
			models[i][key] = m
		}
		m.Calls++
		m.Tokens.add(u)
		modelNames[u.Model] = true
	})
	if err != nil {
		return statsHistoryView{}, err
	}
	empty := newConvertedCost(tc, cache).Summary()
	for i := range out.Series {
		d := &out.Series[i]
		if costs[i] != nil {
			d.Cost = costs[i].Summary()
		} else {
			d.Cost = empty
		}
		if d.TTFTCalls > 0 {
			d.AvgTTFTMS = ttftSum[i] / int64(d.TTFTCalls)
		}
		d.Models = make([]statsHistoryModel, 0, len(models[i]))
		for _, m := range models[i] {
			d.Models = append(d.Models, *m)
		}
		sort.Slice(d.Models, func(a, b int) bool {
			if d.Models[a].Model != d.Models[b].Model {
				return d.Models[a].Model < d.Models[b].Model
			}
			return d.Models[a].Provider < d.Models[b].Provider
		})
	}
	out.Total = total.Summary()
	for name := range modelNames {
		out.Models = append(out.Models, name)
	}
	sort.Strings(out.Models)

	toolUS := map[string]int64{}
	err = store.VisitTurnPhases(ctx, start, func(at time.Time, phases map[string]int64) {
		i, ok := index[at.In(loc).Format("2006-01-02")]
		for key, us := range phases {
			name, isTool := session.ToolPhaseName(key)
			if !isTool || us <= 0 {
				continue
			}
			toolUS[name] += us
			if ok {
				out.Series[i].ToolTimeMS += us / 1000
			}
		}
	})
	if err != nil {
		return statsHistoryView{}, err
	}
	for _, tt := range session.SortToolTimes(toolUS) {
		out.Tools = append(out.Tools, statsHistoryTool{Name: tt.Name, TimeMS: tt.US / 1000})
	}
	return out, nil
}

// handleStatsHistory serves GET /api/stats/history?days=N (default 30,
// max 90): daily usage series for the Usage panel charts.
func (s *Server) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := s.eng.statsHistory(r.Context(), queryInt(r, "days", statsHistoryDefaultDays), time.Now(), time.Local)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}
