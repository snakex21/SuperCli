package webgui

import (
	"context"
	"errors"
	"reflect"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"testing"
)

func TestReadStatsUsagePreservesFullReadAggregates(t *testing.T) {
	_, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Provider: "manual", Model: "fixture", InputCost: 3.14, CachedInputCost: 1.23, OutputCost: 7.1}}}
	for _, seq := range []int{7, 1, 3, 2, 6, 4, 5} {
		u := session.UsageRecord{SessionID: sess.ID, CallSeq: seq, Provider: "manual", ProviderType: config.ProviderOpenAI, EndpointHost: "fixture.invalid", Model: "fixture", Input: int64(seq * 123), Output: int64(seq * 21), CachedInput: int64(seq * 7), Reasoning: int64(seq * 3), HasCachedInput: seq%2 == 0, HasReasoning: seq%3 == 0, ContextWindow: seq * 10000, ContextSystem: seq * 10, ContextUser: seq * 20, ContextAssistant: seq * 30, ContextTool: seq * 40, ContextOther: seq * 50, TTFTMS: 15, PrefillBudgetSource: "unused field", Source: "fixture"}
		if seq == 3 {
			u.ProviderType = config.ProviderCodex
			u.Provider = "included"
		}
		if seq == 4 {
			u.Provider = "unknown"
		}
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ReadUsage(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readStatsUsage(ctx, store, sess.ID, tc)
	if err != nil {
		t.Fatal(err)
	}
	var wantTokens statsTokensView
	for _, u := range rows {
		wantTokens.Input += u.Input
		wantTokens.Output += u.Output
		wantTokens.CachedInput += u.CachedInput
		wantTokens.Reasoning += u.Reasoning
		wantTokens.HasCached = wantTokens.HasCached || u.HasCachedInput
		wantTokens.HasReasoning = wantTokens.HasReasoning || u.HasReasoning
	}
	if got.Calls != len(rows) || got.Tokens != wantTokens || !reflect.DeepEqual(got.Cost, resolveStatsCost(tc, rows, session.UsageRecord{})) {
		t.Fatalf("aggregate differs: %+v", got)
	}
	last := rows[len(rows)-1]
	if contextFromUsage(got.Last) != contextFromUsage(last) || got.Last.Provider != last.Provider || got.Last.ProviderType != last.ProviderType || got.Last.Model != last.Model || got.Last.EndpointHost != last.EndpointHost {
		t.Fatalf("last call differs: %+v want %+v", got.Last, last)
	}
	empty, err := readStatsUsage(ctx, store, "missing", tc)
	if err != nil || empty.Calls != 0 || empty.Cost.State != "unknown" {
		t.Fatalf("empty %+v %v", empty, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	partial, err := readStatsUsage(canceled, store, sess.ID, tc)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(partial, statsUsageRead{}) {
		t.Fatalf("failed read returned partial totals: %+v %v", partial, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	failed, err := readStatsUsage(ctx, store, sess.ID, tc)
	if err == nil || !reflect.DeepEqual(failed, statsUsageRead{}) {
		t.Fatalf("closed store returned partial totals: %+v %v", failed, err)
	}
}
