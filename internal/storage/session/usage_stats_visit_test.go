package session

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestVisitUsageForStatsPreservesProjectionOrderAndIsolation(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/fixture")
	other := mustCreateUsageSession(t, s, "/other")
	ctx := context.Background()
	for _, seq := range []int{3, 1, 2} {
		u := UsageRecord{SessionID: sess.ID, CallSeq: seq, Provider: "fixture", ProviderType: "openai-compatible", EndpointHost: "fixture.invalid", Model: "model",
			Input: int64(seq * 100), Output: int64(seq * 10), CachedInput: int64(seq * 5), Reasoning: int64(seq), HasCachedInput: seq%2 == 0, HasReasoning: seq%2 == 1,
			ContextWindow: seq * 10000, ContextSystem: seq * 11, ContextUser: seq * 12, ContextAssistant: seq * 13, ContextTool: seq * 14, ContextOther: seq * 15,
			TTFTMS: 123, DurationMS: int64(seq * 1000), HasTiming: seq != 2, PrefillEvaluated: 456, PrefillTokensPerSecond: 7.89, PrefillBudget: 999, PrefillBudgetSource: "budget", Source: "source", CreatedAt: time.Unix(1000, 0)}
		if err := s.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: other.ID, Input: 999}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadUsage(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []UsageRecord
	if err := s.VisitUsageForStats(ctx, sess.ID, func(u UsageRecord) { got = append(got, u) }); err != nil {
		t.Fatal(err)
	}
	want := make([]UsageRecord, len(before))
	for i, u := range before {
		want[i] = UsageRecord{Provider: u.Provider, ProviderType: u.ProviderType, EndpointHost: u.EndpointHost, Model: u.Model, Input: u.Input, Output: u.Output, CachedInput: u.CachedInput, Reasoning: u.Reasoning, HasCachedInput: u.HasCachedInput, HasReasoning: u.HasReasoning, TTFTMS: u.TTFTMS, DurationMS: u.DurationMS, HasTiming: u.HasTiming, ContextWindow: u.ContextWindow, ContextSystem: u.ContextSystem, ContextUser: u.ContextUser, ContextAssistant: u.ContextAssistant, ContextTool: u.ContextTool, ContextOther: u.ContextOther, Source: u.Source}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection/order mismatch: %+v want %+v", got, want)
	}
	after, err := s.ReadUsage(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("read changed storage: %v", err)
	}
	visits := 0
	if err := s.VisitUsageForStats(ctx, "missing", func(UsageRecord) { visits++ }); err != nil || visits != 0 {
		t.Fatalf("missing session: %d %v", visits, err)
	}
}

func TestVisitUsageForStatsCancellationAndQueryErrors(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/fixture")
	for i := 0; i < 20; i++ {
		if err := s.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	visits := 0
	if err := s.VisitUsageForStats(ctx, sess.ID, func(UsageRecord) { visits++ }); !errors.Is(err, context.Canceled) || visits != 0 {
		t.Fatalf("precanceled: %d %v", visits, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	err := s.VisitUsageForStats(ctx, sess.ID, func(UsageRecord) { visits++; cancel() })
	if !errors.Is(err, context.Canceled) || visits == 0 || visits >= 20 {
		t.Fatalf("mid-read cancel: %d %v", visits, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.VisitUsageForStats(context.Background(), sess.ID, func(UsageRecord) { t.Fatal("callback after close") }); err == nil {
		t.Fatal("closed store ignored")
	}
}
