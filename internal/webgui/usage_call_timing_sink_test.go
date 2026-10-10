package webgui

import (
	"context"
	"supercli/internal/llm"
	"testing"
	"time"
)

func TestGUIUsageSinkKeepsHelperTimingInDurableJournal(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "selected-main", "")
	if err != nil {
		t.Fatal(err)
	}
	sink := s.eng.usageCallSink(store, sess.ID)
	sink(llm.CallStat{Purpose: llm.PurposeCompact, Model: "helper-qwen", TokensIn: 100, TokensOut: 80, TokensReasoning: 60, TTFT: 2 * time.Second, Duration: 6 * time.Second})
	sink(llm.CallStat{Purpose: llm.PurposeMain, Model: "selected-main", TokensIn: 100, TokensOut: 20, Canceled: true, TTFT: time.Second, Duration: 2 * time.Second})
	got, err := s.eng.usage(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Totals.TimingReportedCalls != 1 || got.Totals.StreamOutputTokens != 80 || got.Totals.StreamMS != 4000 {
		t.Fatalf("GUI timing not persisted or canceled sample included: %+v", got)
	}
	var helper *usageRow
	for i := range got.Rows {
		if got.Rows[i].Model == "helper-qwen" {
			helper = &got.Rows[i]
		}
	}
	if helper == nil || helper.DurationMS != 6000 || helper.TTFTMS != 2000 || helper.Purposes[0].Purpose != "compact" {
		t.Fatalf("helper timer changed attribution: %+v", helper)
	}
}
