package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestStatsHistoryDaysModelsAndTools(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	loc := time.FixedZone("T", 2*3600)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, loc)
	twoDaysAgo := now.Add(-48 * time.Hour)
	records := []session.UsageRecord{
		{SessionID: sess.ID, Provider: "openai", Model: "gpt-a", Input: 1000, CachedInput: 600, Output: 50, Reasoning: 10, HasCachedInput: true, HasReasoning: true, TTFTMS: 300, DurationMS: 2000, HasTiming: true, CreatedAt: now},
		{SessionID: sess.ID, Provider: "openai", Model: "gpt-b", Input: 200, Output: 20, TTFTMS: 500, DurationMS: 1000, HasTiming: true, CreatedAt: now.Add(time.Minute)},
		{SessionID: sess.ID, Provider: "openai", Model: "gpt-a", Input: 10, Output: 1, CreatedAt: twoDaysAgo},
		{SessionID: sess.ID, Provider: "openai", Model: "gpt-old", Input: 10, Output: 1, CreatedAt: now.Add(-20 * 24 * time.Hour)},
	}
	for _, u := range records {
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, turn := range []session.TurnSummary{
		{SessionID: sess.ID, Phases: map[string]int64{"tool:read_file": 5_000, "tool:web_fetch": 40_000, "model": 9}, CreatedAt: now},
		{SessionID: sess.ID, Phases: map[string]int64{"tool:ctx_execute": 1_000, "tool:read_file": 2_000, "tool:a": 1, "tool:b": 1, "tool:c": 1, "tool:d": 1}, CreatedAt: twoDaysAgo},
	} {
		appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleUser, Content: "q"})
		appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "a"})
		if err := store.AppendTurnSummary(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	got, err := eng.statsHistory(ctx, 7, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 || len(got.Series) != 7 || got.From != "2026-10-06" || got.To != "2026-10-12" {
		t.Fatalf("window = %d %s..%s len=%d", got.Days, got.From, got.To, len(got.Series))
	}
	today := got.Series[6]
	if today.Calls != 2 || today.Tokens.Evaluated != 600 || today.Tokens.Cached != 600 || today.Tokens.Output != 70 || today.Tokens.Reasoning != 10 {
		t.Fatalf("today = %+v", today)
	}
	if today.AvgTTFTMS != 400 || today.ModelTimeMS != 3000 || today.ToolTimeMS != 45 {
		t.Fatalf("today timing = %+v", today)
	}
	if len(today.Models) != 2 || today.Models[0].Model != "gpt-a" || today.Models[0].Tokens.Evaluated != 400 || today.Models[1].Model != "gpt-b" {
		t.Fatalf("today models = %+v", today.Models)
	}
	if got.Series[4].Calls != 1 || got.Series[5].Calls != 0 || len(got.Series[5].Models) != 0 {
		t.Fatalf("other days = %+v / %+v", got.Series[4], got.Series[5])
	}
	if len(got.Models) != 2 || got.Models[0] != "gpt-a" || got.Models[1] != "gpt-b" {
		t.Fatalf("models = %v", got.Models)
	}
	// All tools, not top 5, longest first.
	if len(got.Tools) != 7 || got.Tools[0].Name != "web_fetch" || got.Tools[0].TimeMS != 40 || got.Tools[1].Name != "read_file" || got.Tools[1].TimeMS != 7 {
		t.Fatalf("tools = %+v", got.Tools)
	}
}

func TestStatsHistoryHTTPEmptyAndClamped(t *testing.T) {
	eng, _, _, _ := statsFixture(t)
	srv := NewServer(eng, false)
	for _, tc := range []struct {
		query string
		days  int
	}{{"", 30}, {"?days=7", 7}, {"?days=500", 90}, {"?days=bogus", 30}} {
		rec := httptest.NewRecorder()
		srv.handleStatsHistory(rec, httptest.NewRequest(http.MethodGet, "/api/stats/history"+tc.query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", tc.query, rec.Code, rec.Body.String())
		}
		var view statsHistoryView
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if view.Days != tc.days || len(view.Series) != tc.days || len(view.Tools) != 0 || view.Models == nil {
			t.Fatalf("%s view days=%d series=%d tools=%v models=%v", tc.query, view.Days, len(view.Series), view.Tools, view.Models)
		}
		for _, d := range view.Series {
			if d.Calls != 0 || d.Models == nil {
				t.Fatalf("non-empty day %+v", d)
			}
		}
	}
	rec := httptest.NewRecorder()
	srv.handleStatsHistory(rec, httptest.NewRequest(http.MethodPost, "/api/stats/history", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rec.Code)
	}
}
