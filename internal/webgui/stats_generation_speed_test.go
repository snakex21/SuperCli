package webgui

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func requireStatsGenerationSpeed(t *testing.T, got statsGenerationSpeedView, output, streamMS int64, samples, calls int) {
	t.Helper()
	if got.Scope != "session" || got.OutputTokens != output || got.StreamMS != streamMS || got.Samples != samples || got.Calls != calls {
		t.Fatalf("wrong sample coverage: %+v", got)
	}
	if samples == 0 {
		if got.TokensPerSecond != nil {
			t.Fatalf("unknown speed became a number: %+v", got)
		}
		return
	}
	want := float64(output) * 1000 / float64(streamMS)
	if got.TokensPerSecond == nil || math.Abs(*got.TokensPerSecond-want) > 1e-9 {
		t.Fatalf("speed is not ratio of matching sums: %+v want %g", got, want)
	}
}

func TestStatsGenerationSpeedSurvivesResumeAndUnmeasuredLatestCall(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	ctx := context.Background()
	for _, u := range []session.UsageRecord{
		{Provider: "one", Model: "fast", Source: "main", Input: 100, Output: 100, CachedInput: 80, Reasoning: 60, HasCachedInput: true, HasReasoning: true, HasTiming: true, TTFTMS: 1000, DurationMS: 3000},
		{Provider: "two", Model: "slow-helper", Source: "compact", Input: 200, Output: 30, Reasoning: 20, HasReasoning: true, HasTiming: true, TTFTMS: 2000, DurationMS: 8000},
		{Provider: "three", Model: "latest-unmeasured", Source: "main", Input: 300, Output: 10000, TTFTMS: 500},
		{Source: "legacy", Output: 40000, HasTiming: true, TTFTMS: 1000, DurationMS: 2000},
	} {
		u.SessionID = sess.ID
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	check := func(e *Engine) {
		t.Helper()
		got, err := e.stats(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		requireStatsGenerationSpeed(t, got.GenerationSpeed, 130, 8000, 2, 3)
		if got.LastTurn == nil || got.LastTurn.Event.Model != "latest-unmeasured" {
			t.Fatalf("last call should keep its own identity: %+v", got.LastTurn)
		}
		popup, err := e.usage(ctx, sess.ID)
		if err != nil || popup.Totals.StreamOutputTokens != got.GenerationSpeed.OutputTokens || popup.Totals.StreamMS != got.GenerationSpeed.StreamMS || popup.Totals.StreamReportedCalls != got.GenerationSpeed.Samples {
			t.Fatalf("panel and popup disagree: %+v %v", popup.Totals, err)
		}
	}
	check(eng)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	check(resumed)
	resumed.mu.Lock()
	resumed.cfg.Model = "newly-selected-model"
	resumed.mu.Unlock()
	check(resumed)
	other, err := store.Create(dir, "other", "unmeasured conversation")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{other.ID, ""} {
		got, err := resumed.stats(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		requireStatsGenerationSpeed(t, got.GenerationSpeed, 0, 0, 0, 0)
		raw, _ := json.Marshal(got.GenerationSpeed)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if fields["tokens_per_second"] != nil || fields["scope"] != "session" {
			t.Fatalf("new session should report explicit unknown: %s", raw)
		}
	}
}

func TestStatsGenerationSpeedDoesNotUseFailedOrCanceledClocks(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	sink := eng.usageCallSink(store, sess.ID)
	for _, call := range []llm.CallStat{
		{Purpose: llm.PurposeMain, Model: "measured", TokensIn: 100, TokensOut: 40, TTFT: time.Second, Duration: 3 * time.Second},
		{Purpose: llm.PurposeMain, Model: "failed", TokensIn: 100, TokensOut: 9000, TTFT: time.Second, Duration: 2 * time.Second, Failed: true},
		{Purpose: llm.PurposeCompact, Model: "canceled", TokensIn: 100, TokensOut: 8000, TTFT: time.Second, Duration: 2 * time.Second, Canceled: true},
	} {
		sink(call)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireStatsGenerationSpeed(t, got.GenerationSpeed, 40, 2000, 1, 3)
	if got.Tokens.Output != 17040 {
		t.Fatal("unmeasured billed output should remain in usage totals")
	}
}

func TestMeasuredUsageStreamRejectsUnknownAndInvalidClocks(t *testing.T) {
	for _, u := range []session.UsageRecord{
		{Output: 100, DurationMS: 2000, TTFTMS: 1000},
		{Output: 100, HasTiming: true, DurationMS: 2000},
		{Output: 100, HasTiming: true, DurationMS: 2000, TTFTMS: -1},
		{Output: 100, HasTiming: true, DurationMS: 0, TTFTMS: 1},
		{Output: 100, HasTiming: true, DurationMS: -1, TTFTMS: 1},
		{Output: 100, HasTiming: true, DurationMS: 2000, TTFTMS: 2000},
		{Output: 100, HasTiming: true, DurationMS: 2000, TTFTMS: 3000},
		{Output: 0, HasTiming: true, DurationMS: 2000, TTFTMS: 1000},
		{Output: -1, HasTiming: true, DurationMS: 2000, TTFTMS: 1000},
		{Source: "legacy", Output: 100, HasTiming: true, DurationMS: 2000, TTFTMS: 1000},
	} {
		if output, ms, known := measuredUsageStream(u); known || output != 0 || ms != 0 {
			t.Fatalf("invented a measured stream for %+v", u)
		}
	}
}
