package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/account/usagecost"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestUsageDetailsKeepsPurposeSubsetsCoverageAndLegacySeparate(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "last-selector-is-not-legacy-attribution", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []session.UsageRecord{
		{Provider: "p", Model: "a", Source: "model", Input: 100, Output: 40, CachedInput: 25, Reasoning: 10,
			HasCachedInput: true, HasReasoning: true, ContextSystem: 3, ContextTool: 5},
		{Provider: "q", Model: "b", Source: "compact", Input: 20, Output: 12, Reasoning: 6,
			HasReasoning: true, ContextOther: 8},
		{Provider: "p", Model: "a", Source: "worker", Input: 50, Output: 15},
		{Provider: "uncertain", Model: "old-selector", Source: "legacy", Input: 200, Output: 40},
	} {
		u.SessionID = sess.ID
		price := usagecost.FreezeQuote(config.TomlConfig{}, u, u.Source == "legacy")
		u.PriceSnapshot = &price
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	// Older aggregate counters can exceed detailed usage. Report the gap
	// without fabricating model calls or altering the durable ledger.
	if err := store.TryUpdateUsage(context.Background(), sess.ID, 400, 130); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/usage?session="+sess.ID, nil)
	req.Host, req.RemoteAddr = "127.0.0.1", "127.0.0.1:1234"
	s.Handler().ServeHTTP(rec, req)
	var got usageView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || got.Scope != "session" || got.SessionID != sess.ID {
		t.Fatalf("API status=%d result=%+v", rec.Code, got)
	}
	if got.Totals.Calls != 3 || got.Totals.LegacyRecords != 1 || got.Totals.Input != 370 || got.Totals.Output != 107 ||
		got.Totals.Total != 477 || got.Totals.CachedInput != 25 || got.Totals.Reasoning != 16 || got.Totals.EvaluatedInput != 345 {
		t.Fatalf("subsets or synthetic aggregate double counted: %+v", got.Totals)
	}
	if got.Totals.CachedReportedCalls != 1 || got.Totals.ReasoningReportedCalls != 2 ||
		got.Totals.ContextEstimateCalls != 2 || got.Totals.ContextToolEstimate != 5 ||
		!got.Totals.ContextToolEstimateKnown || got.Totals.ContextEstimateSource != "request-shape" {
		t.Fatalf("coverage fabricated: %+v", got.Totals)
	}
	if len(got.Rows) != 2 || len(got.Purposes) != 3 || got.Rows[0].Provider != "p" || got.Rows[0].Model != "a" ||
		got.Rows[0].Calls != 2 || got.Rows[0].Total != 205 || len(got.Rows[0].Purposes) != 2 ||
		got.Rows[0].Purposes[0].Purpose != "main" || got.Rows[0].Purposes[1].Purpose != "task" {
		t.Fatalf("model/purpose attribution=%+v purposes=%+v", got.Rows, got.Purposes)
	}
	compact := got.Rows[1]
	if compact.ContextToolEstimate != 0 || !compact.ContextToolEstimateKnown || compact.ContextEstimateCalls != 1 ||
		compact.HasCached || !compact.HasReasoning || compact.Purposes[0].Purpose != "compact" {
		t.Fatalf("known zero confused with unavailable context/cache: %+v", compact)
	}
	if got.LegacyUnattributed.Input != 200 || got.LegacyUnattributed.Output != 40 ||
		got.LegacyUnattributed.Total != 240 || got.LegacyUnattributed.Sessions != 1 {
		t.Fatalf("legacy accounting coverage=%+v", got.LegacyUnattributed)
	}
	if got.LegacyGap.Input != 30 || got.LegacyGap.Output != 23 || got.LegacyGap.Total != 53 || got.LegacyGap.Sessions != 1 ||
		got.Rows[0].Total+got.Rows[1].Total+got.LegacyUnattributed.Total != got.Totals.Total {
		t.Fatalf("legacy discrepancies mixed with accounted usage: %+v", got)
	}
	all, err := s.eng.usage(context.Background(), "")
	if err != nil || all.Scope != "history" || all.Totals != got.Totals {
		t.Fatalf("history scope changed: %+v err=%v", all, err)
	}
	before := all.Totals
	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	all, err = s.eng.usage(context.Background(), "")
	if err != nil || all.Totals != before || len(all.Rows) != 2 || all.LegacyUnattributed.Input != 200 {
		t.Fatalf("deleted conversation erased actual history: %+v err=%v", all, err)
	}
}

func TestUsageDetailsRejectsMutationAndCrossWorkspaceSession(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(s.eng.Home()+"/other-workspace", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/usage", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/usage?session=" + other.ID, http.StatusBadRequest},
		{http.MethodGet, "/api/usage?session=missing", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		s.handleUsage(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestStatsRestoresSavedResponseWithoutRelabelingItWithLaterCalls(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "current-selector", "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC)
	for i, u := range []session.UsageRecord{
		{Source: "main", Model: "response-model", Input: 100, Output: 30, CreatedAt: at},
		{Source: "title", Model: "title-model", Input: 50, Output: 5, CreatedAt: at.Add(3 * time.Second)},
		{Source: "main", Model: "later-interrupted-model", Input: 60, Output: 0, CreatedAt: at.Add(4 * time.Second)},
	} {
		u.SessionID, u.CallSeq = sess.ID, i+1
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendMessage(context.Background(), sess.ID, session.Encoded{Role: "assistant", Content: "saved response"}); err != nil {
		t.Fatal(err)
	}
	want := session.TurnSummary{SessionID: sess.ID, DurationMS: 4567, Input: 100, Output: 30,
		CachedInput: 10, Reasoning: 12, HasCachedInput: true, HasReasoning: true, ToolCalls: 2, CreatedAt: at.Add(2 * time.Second)}
	if err := store.AppendTurnSummary(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := s.eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		last := got.LastTurn
		if last == nil || last.Kind != "response" || last.Event.Model != "response-model" || last.Event.Input != 100 ||
			last.Event.Total != 130 || last.Event.Reasoning != 12 || last.Event.CachedInput != 10 ||
			!last.Event.HasCached || !last.Event.HasReasoning || last.Tools != 2 || last.Elapsed != 4567 {
			t.Fatalf("persisted response lost/relabelled: %+v", last)
		}
		data, err := json.Marshal(last)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		_ = json.Unmarshal(data, &raw)
		if _, exists := raw["generation_tps"]; exists {
			t.Fatal("response duration fabricated generation speed")
		}
	}
}

func TestStatsLastTurnDistinguishesUnsummarizedCallAndUnknownResponseModel(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"empty", "model_call", "response"} {
		sess, err := store.Create(s.eng.Home(), "selector-not-an-observed-model", "")
		if err != nil {
			t.Fatal(err)
		}
		if kind == "model_call" {
			if err := store.AppendUsage(context.Background(), session.UsageRecord{SessionID: sess.ID, Source: "main", Model: "observed", Input: 9, Output: 2}); err != nil {
				t.Fatal(err)
			}
		}
		if kind == "response" {
			if err := store.AppendTurnSummary(context.Background(), session.TurnSummary{SessionID: sess.ID, AssistantSeq: 1, Input: 7, Output: 3}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "empty" {
			if got.LastTurn != nil {
				t.Fatalf("empty session fabricated last turn: %+v", got.LastTurn)
			}
		} else if got.LastTurn == nil || got.LastTurn.Kind != kind {
			t.Fatalf("missing %s: %+v", kind, got.LastTurn)
		} else if kind == "model_call" && (got.LastTurn.Event.Model != "observed" || got.LastTurn.Elapsed != 0 || got.LastTurn.Tools != 0) {
			t.Fatalf("single call fabricated response telemetry: %+v", got.LastTurn)
		} else if kind == "response" && got.LastTurn.Event.Model != "" {
			t.Fatalf("old unknown response used current selector: %+v", got.LastTurn)
		}
	}
}
