package webgui

import (
	"context"
	"net/http"
	"sort"

	"supercli/internal/storage/session"
)

// Provider cache and reasoning counts are subsets, not extra billable tokens.
// Tool context is the estimated tool-role input carried by model requests; it
// is neither tool-call output nor additional usage to add to Total.
type usageCounters struct {
	Calls                    int    `json:"calls"`
	Input                    int64  `json:"input"`
	EvaluatedInput           int64  `json:"evaluated_input"`
	CachedInput              int64  `json:"cached_input"`
	Output                   int64  `json:"output"`
	Reasoning                int64  `json:"reasoning"`
	Total                    int64  `json:"total"`
	HasCached                bool   `json:"has_cached"`
	HasReasoning             bool   `json:"has_reasoning"`
	CachedReportedCalls      int    `json:"cached_reported_calls"`
	ReasoningReportedCalls   int    `json:"reasoning_reported_calls"`
	ContextToolEstimate      int64  `json:"context_tool_estimate"`
	ContextToolEstimateKnown bool   `json:"context_tool_estimate_known"`
	ContextEstimateCalls     int    `json:"context_estimate_calls"`
	ContextEstimateSource    string `json:"context_estimate_source"`
	LegacyRecords            int    `json:"legacy_records"`
	HasTiming                bool   `json:"has_timing"`
	TimingReportedCalls      int    `json:"timing_reported_calls"`
	DurationMS               int64  `json:"duration_ms"`
	TTFTMS                   int64  `json:"ttft_ms"`
	TTFTReportedCalls        int    `json:"ttft_reported_calls"`
	StreamMS                 int64  `json:"stream_ms"`
	StreamOutputTokens       int64  `json:"stream_output_tokens"`
	StreamReportedCalls      int    `json:"stream_reported_calls"`
}

type usagePurpose struct {
	Purpose string `json:"purpose"`
	usageCounters
}

type usageRow struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	usageCounters
	Purposes []usagePurpose `json:"purposes"`
}

type legacyUsageView struct {
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Total    int64 `json:"total"`
	Sessions int   `json:"sessions"`
}

type usageView struct {
	SessionID          string          `json:"session_id"`
	Scope              string          `json:"scope"`
	Totals             usageCounters   `json:"totals"`
	Rows               []usageRow      `json:"rows"`
	Purposes           []usagePurpose  `json:"purposes"`
	LegacyUnattributed legacyUsageView `json:"legacy_unattributed"`
	LegacyGap          legacyUsageView `json:"legacy_gap"`
}

func normalizedUsagePurpose(source string) string {
	switch source {
	case "", "model", "main":
		return "main"
	case "worker", "task":
		return "task"
	default:
		return source
	}
}

func (c *usageCounters) add(u session.UsageRecord) {
	input, output := max(u.Input, 0), max(u.Output, 0)
	cached, reasoning := min(max(u.CachedInput, 0), input), min(max(u.Reasoning, 0), output)
	c.Input += input
	c.Output += output
	c.CachedInput += cached
	c.Reasoning += reasoning
	c.Total += input + output
	c.EvaluatedInput += input - cached
	if u.Source == "legacy" {
		c.LegacyRecords++
		return
	}
	c.Calls++
	// These are sums of measured calls, not elapsed session time: independent
	// parallel calls can overlap. TTFT includes backend/transport waiting and
	// is not a direct measurement of prompt processing or hidden reasoning.
	if u.HasTiming && u.DurationMS > 0 && u.TTFTMS >= 0 && u.TTFTMS <= u.DurationMS {
		c.HasTiming = true
		c.TimingReportedCalls++
		c.DurationMS += u.DurationMS
		if u.TTFTMS > 0 {
			c.TTFTMS += u.TTFTMS
			c.TTFTReportedCalls++
			if streamOutput, streamMS, known := measuredUsageStream(u); known {
				c.StreamMS += streamMS
				c.StreamOutputTokens += streamOutput
				c.StreamReportedCalls++
			}
		}
	}
	if u.HasCachedInput {
		c.HasCached = true
		c.CachedReportedCalls++
	}
	if u.HasReasoning {
		c.HasReasoning = true
		c.ReasoningReportedCalls++
	}
	if u.ContextEstimateKnown {
		c.ContextToolEstimate += int64(max(u.ContextTool, 0))
		c.ContextToolEstimateKnown = true
		c.ContextEstimateCalls++
		c.ContextEstimateSource = "request-shape"
	}
}

func sortedUsagePurposes(groups map[string]*usageCounters) []usagePurpose {
	out := make([]usagePurpose, 0, len(groups))
	for purpose, counters := range groups {
		out = append(out, usagePurpose{Purpose: purpose, usageCounters: *counters})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Purpose < out[j].Purpose })
	return out
}

// usage streams the durable journal only when the popup is requested. It
// performs no inference, price lookup, FX lookup or transcript reconstruction.
func (e *Engine) usage(ctx context.Context, sessionID string) (usageView, error) {
	out := usageView{SessionID: sessionID, Scope: "history", Rows: []usageRow{}, Purposes: []usagePurpose{}}
	store, err := e.sessionStore()
	if err != nil {
		return out, err
	}
	if sessionID != "" {
		meta, err := store.Get(sessionID)
		if err != nil {
			return out, err
		}
		if !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return out, errSessionOutsideWorkspace
		}
		out.Scope = "session"
	}
	type group struct {
		row      usageRow
		purposes map[string]*usageCounters
	}
	groups := make(map[[2]string]*group)
	purposes := make(map[string]*usageCounters)
	legacySessions := make(map[string]bool)
	err = store.VisitTokenUsage(ctx, sessionID, func(u session.UsageRecord) {
		out.Totals.add(u)
		if u.Source == "legacy" {
			// A pre-ledger session's last selected model is not reliable call
			// attribution. Preserve its real aggregate outside the model table.
			out.LegacyUnattributed.Input += max(u.Input, 0)
			out.LegacyUnattributed.Output += max(u.Output, 0)
			legacySessions[u.SessionID] = true
			return
		}
		key := [2]string{u.Provider, u.Model}
		g := groups[key]
		if g == nil {
			g = &group{row: usageRow{Provider: u.Provider, Model: u.Model}, purposes: make(map[string]*usageCounters)}
			groups[key] = g
		}
		g.row.add(u)
		purpose := normalizedUsagePurpose(u.Source)
		if g.purposes[purpose] == nil {
			g.purposes[purpose] = new(usageCounters)
		}
		g.purposes[purpose].add(u)
		if purposes[purpose] == nil {
			purposes[purpose] = new(usageCounters)
		}
		purposes[purpose].add(u)
	})
	if err != nil {
		return out, err
	}
	if err := store.VisitLegacyUsageGaps(ctx, sessionID, func(gap session.LegacyUsageGap) {
		out.LegacyGap.Input += gap.Input
		out.LegacyGap.Output += gap.Output
		out.LegacyGap.Sessions++
	}); err != nil {
		return out, err
	}
	out.LegacyUnattributed.Total = out.LegacyUnattributed.Input + out.LegacyUnattributed.Output
	out.LegacyUnattributed.Sessions = len(legacySessions)
	out.LegacyGap.Total = out.LegacyGap.Input + out.LegacyGap.Output
	for _, g := range groups {
		g.row.Purposes = sortedUsagePurposes(g.purposes)
		out.Rows = append(out.Rows, g.row)
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].Provider == out.Rows[j].Provider {
			return out.Rows[i].Model < out.Rows[j].Model
		}
		return out.Rows[i].Provider < out.Rows[j].Provider
	})
	out.Purposes = sortedUsagePurposes(purposes)
	return out, nil
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := s.eng.usage(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}
