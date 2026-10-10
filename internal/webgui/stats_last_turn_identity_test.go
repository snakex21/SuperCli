package webgui

import (
	"context"
	"testing"
	"time"

	"supercli/internal/storage/session"
)

func TestStatsLastTurnDoesNotInheritUnmeasuredResponseModel(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "selector-A", "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(context.Background(), session.UsageRecord{
		SessionID: sess.ID, Source: "main", Model: "A", Input: 8, Output: 2, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnSummary(context.Background(), session.TurnSummary{
		SessionID: sess.ID, AssistantSeq: 1, DurationMS: 2000, Input: 8, Output: 2, CreatedAt: at.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	for _, duration := range []int64{1000, 0} {
		// No main call belongs to response two; a helper inside its span and
		// a real earlier response are both insufficient to identify it.
		if err := store.AppendTurnSummary(context.Background(), session.TurnSummary{
			SessionID: sess.ID, AssistantSeq: 2, DurationMS: duration, Input: 15, Output: 4, CreatedAt: at.Add(10 * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendUsage(context.Background(), session.UsageRecord{
			SessionID: sess.ID, Source: "compact", Model: "B", Input: 7, Output: 2, CreatedAt: at.Add(9500 * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
		got, err := s.eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastTurn == nil || got.LastTurn.Kind != "response" || got.LastTurn.Event.Model != "" ||
			got.LastTurn.Event.Input != 15 || got.LastTurn.Event.Output != 4 {
			t.Fatalf("unmeasured response inherited a previous/helper model: %+v", got.LastTurn)
		}
	}
}
