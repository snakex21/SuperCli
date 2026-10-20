package webgui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"supercli/internal/llm"
)

// Deliberately closed projection: never serialize UsageRecord or CallStat here.
type recentUsageTokens struct {
	In            int64 `json:"in"`
	Out           int64 `json:"out"`
	Cache         int64 `json:"cache"`
	CacheReported bool  `json:"cache_reported"`
}

type recentUsageRow struct {
	Seq          int               `json:"seq"`
	CreatedAt    time.Time         `json:"created_at"`
	Purpose      string            `json:"purpose"`
	Tokens       recentUsageTokens `json:"tokens"`
	DurationMS   int64             `json:"duration_ms"`
	TTFTMS       int64             `json:"ttft_ms"`
	HasTiming    bool              `json:"has_timing"`
	RequestTrace *llm.RequestTrace `json:"request_trace"`
}

type recentUsageView struct {
	SessionID string           `json:"session_id"`
	Rows      []recentUsageRow `json:"rows"`
}

func recentUsagePurpose(source string) string {
	purpose := normalizedUsagePurpose(source)
	switch purpose {
	case llm.PurposeMain, llm.PurposeNavigator, llm.PurposeCompact,
		llm.PurposeReflect, llm.PurposeDraft, llm.PurposeVerdict,
		llm.PurposeMemory, llm.PurposeGoal, llm.PurposeConsult,
		llm.PurposeJudge, llm.PurposeDarwin, llm.PurposeProbe,
		llm.PurposeTitle, llm.PurposeTask, llm.PurposeAdvisor:
		return purpose
	default:
		return "other"
	}
}

func (e *Engine) recentUsage(ctx context.Context, sessionID string, limit int) (recentUsageView, error) {
	out := recentUsageView{SessionID: sessionID, Rows: []recentUsageRow{}}
	if sessionID == "" {
		return out, fmt.Errorf("session is required for recent usage")
	}
	store, err := e.sessionStore()
	if err != nil {
		return out, err
	}
	meta, err := store.Get(sessionID)
	if err != nil {
		return out, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return out, errSessionOutsideWorkspace
	}
	rows, err := store.ReadRecentUsage(ctx, sessionID, limit)
	if err != nil {
		return out, err
	}
	for _, u := range rows {
		out.Rows = append(out.Rows, recentUsageRow{
			Seq: u.CallSeq, CreatedAt: u.CreatedAt, Purpose: recentUsagePurpose(u.Source),
			Tokens:     recentUsageTokens{In: u.Input, Out: u.Output, Cache: u.CachedInput, CacheReported: u.HasCachedInput},
			DurationMS: u.DurationMS, TTFTMS: u.TTFTMS, HasTiming: u.HasTiming,
			RequestTrace: llm.SafeRequestTrace(u.RequestTrace),
		})
	}
	return out, nil
}

func (s *Server) handleRecentUsage(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil {
			http.Error(w, "invalid usage limit", http.StatusBadRequest)
			return
		}
	}
	out, err := s.eng.recentUsage(r.Context(), r.URL.Query().Get("session"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}
