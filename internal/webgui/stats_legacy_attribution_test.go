package webgui

import (
	"context"
	"testing"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestLegacyStatsAndCostsKeepTokensPricesWithoutInventingCallsOrModel(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "last-selected-model", "")
	if err != nil {
		t.Fatal(err)
	}
	legacyAmount, helperAmount := 5.0, 1.0
	for _, u := range []session.UsageRecord{
		{Source: "legacy", Provider: "uncertain", Model: "last-selected-model", Input: 100, Output: 10,
			PriceSnapshot: &session.PriceSnapshot{State: "manual", AmountUSD: &legacyAmount, Legacy: true}},
		{Source: "title", Provider: "helper", Model: "title-model", Input: 20, Output: 2,
			PriceSnapshot: &session.PriceSnapshot{State: "manual", AmountUSD: &helperAmount}},
	} {
		u.SessionID = sess.ID
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	read, err := readStatsUsage(context.Background(), store, sess.ID, config.TomlConfig{})
	if err != nil || read.Records != 2 || read.Calls != 1 || read.HasMain ||
		read.Last.Model != "title-model" || read.Main.Model != "" || read.Tokens.Input != 120 || read.Tokens.Output != 12 ||
		read.Cost.Amount == nil || *read.Cost.Amount != 6 || read.Cost.Calls != 1 {
		t.Fatalf("legacy fabricated main call/model or lost persisted cost: %+v err=%v", read, err)
	}
	got, err := s.eng.costs(context.Background(), sess.ID)
	if err != nil || len(got.Rows) != 1 || got.Rows[0].Provider != "helper" || got.Rows[0].Model != "title-model" ||
		got.Rows[0].Calls != 1 || got.Total.Calls != 1 || got.Total.Amount == nil || *got.Total.Amount != 6 {
		t.Fatalf("legacy aggregate got per-model/call attribution: %+v err=%v", got, err)
	}
	old := got.LegacyUnattributed
	if old == nil || old.Provider != "" || old.Model != "" || old.Calls != 0 || old.Total != 110 ||
		old.Cost.Calls != 0 || old.Cost.Amount == nil || *old.Cost.Amount != 5 || old.LegacyCalls != 1 {
		t.Fatalf("legacy frozen amount lost or assigned an observed model: %+v", old)
	}
	stats, err := s.eng.stats(context.Background(), sess.ID)
	if err != nil || stats.SessionToken != 132 || stats.Cost.Amount == nil || *stats.Cost.Amount != 6 {
		t.Fatalf("sidebar discarded old aggregate: %+v err=%v", stats, err)
	}
}
