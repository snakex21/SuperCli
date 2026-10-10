package webgui

import (
	"context"
	"supercli/internal/storage/session"
	"testing"
)

func TestUsageTimingAggregatesOnlyMatchingMeasuredSamples(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "model", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []session.UsageRecord{
		{Source: "compact", Provider: "p", Model: "q", Input: 100, Output: 80, DurationMS: 6000, TTFTMS: 2000, HasTiming: true},
		{Source: "main", Provider: "p", Model: "q", Input: 200, Output: 20, DurationMS: 3000, TTFTMS: 1000, HasTiming: true},
		{Source: "main", Provider: "p", Model: "q", Input: 300, Output: 1000, TTFTMS: 1234},
		{Source: "legacy", Input: 400, Output: 2000, DurationMS: 9000, TTFTMS: 3000, HasTiming: true},
	} {
		u.SessionID = sess.ID
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.eng.usage(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Totals.HasTiming || got.Totals.Calls != 3 || got.Totals.TimingReportedCalls != 2 || got.Totals.DurationMS != 9000 || got.Totals.TTFTMS != 3000 || got.Totals.TTFTReportedCalls != 2 || got.Totals.StreamMS != 6000 || got.Totals.StreamOutputTokens != 100 || got.Totals.StreamReportedCalls != 2 {
		t.Fatalf("unknown, overlapping, or legacy clocks affected timing: %+v", got.Totals)
	}
	compact := got.Rows[0].Purposes[0]
	if compact.Purpose != "compact" || compact.DurationMS != 6000 || compact.StreamMS != 4000 || compact.StreamOutputTokens != 80 || compact.Calls != 1 {
		t.Fatalf("compaction lost separate measurement: %+v", compact)
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	history, err := s.eng.usage(context.Background(), "")
	if err != nil || history.Totals != got.Totals {
		t.Fatalf("deleted session changed measured history: %+v, %v", history, err)
	}
}
