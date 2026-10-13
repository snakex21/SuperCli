package session

import (
	"context"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestReadUsageHistory_DailySeriesAndTools(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sess, err := s.Create(t.TempDir(), "m1", "history")
	if err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("T", 2*3600)
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, loc)
	yesterday := now.Add(-24 * time.Hour)
	old := now.Add(-40 * 24 * time.Hour)
	calls := []UsageRecord{
		{SessionID: sess.ID, Provider: "p", Model: "m1", Input: 100, CachedInput: 40, Output: 10, Reasoning: 3, HasCachedInput: true, HasReasoning: true, TTFTMS: 200, DurationMS: 1000, HasTiming: true, CreatedAt: now},
		{SessionID: sess.ID, Provider: "p", Model: "m2", Input: 50, Output: 5, TTFTMS: 400, DurationMS: 3000, HasTiming: true, CreatedAt: now.Add(time.Minute)},
		{SessionID: sess.ID, Provider: "p", Model: "m1", Input: 7, Output: 1, CreatedAt: yesterday},
		{SessionID: sess.ID, Provider: "p", Model: "m1", Input: 999, Output: 999, CreatedAt: old},
	}
	for _, u := range calls {
		if err := s.AppendUsage(ctx, u); err != nil {
			t.Fatalf("AppendUsage: %v", err)
		}
	}
	turns := []TurnSummary{
		{SessionID: sess.ID, Phases: map[string]int64{"tool:read_file": 2000, "tool:web_fetch": 9000, "model": 5}, CreatedAt: now},
		{SessionID: sess.ID, Phases: map[string]int64{"tool:read_file": 1000}, CreatedAt: yesterday},
		{SessionID: sess.ID, Phases: map[string]int64{"tool:ancient": 1000}, CreatedAt: old},
	}
	writer := NewWriter(s, sess.ID)
	for _, turn := range turns {
		if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "q"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendTurnSummary(ctx, turn); err != nil {
			t.Fatalf("AppendTurnSummary: %v", err)
		}
	}
	h, err := s.ReadUsageHistory(ctx, now, 7, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 7 || h.Days[6].Day != "2026-10-12" || h.Days[5].Day != "2026-10-11" || h.Days[0].Day != "2026-10-06" {
		t.Fatalf("days = %+v", h.Days)
	}
	today := h.Days[6]
	if today.Calls != 2 || today.Input != 150 || today.CachedInput != 40 || today.Output != 15 || today.Reasoning != 3 {
		t.Fatalf("today = %+v", today)
	}
	if today.TTFTCalls != 2 || today.TTFTSumMS != 600 || today.DurationMS != 4000 || today.ToolUS != 11000 {
		t.Fatalf("today timing = %+v", today)
	}
	if h.Days[5].Calls != 1 || h.Days[5].Input != 7 || h.Days[5].ToolUS != 1000 {
		t.Fatalf("yesterday = %+v", h.Days[5])
	}
	if len(h.Tools) != 2 || h.Tools[0].Name != "web_fetch" || h.Tools[0].US != 9000 || h.Tools[1].Name != "read_file" || h.Tools[1].US != 3000 {
		t.Fatalf("tools = %+v", h.Tools)
	}
}

func TestReadUsageHistory_Empty(t *testing.T) {
	s := openTestStore(t)
	h, err := s.ReadUsageHistory(context.Background(), time.Now(), 30, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 30 || len(h.Tools) != 0 {
		t.Fatalf("history = %+v", h)
	}
	for _, d := range h.Days {
		if d.Calls != 0 || d.Total() != 0 {
			t.Fatalf("non-empty day %+v", d)
		}
	}
}
