package app

import (
	"context"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// callStubProvider is a minimal provider for the call-metering tests.
type callStubProvider struct {
	name string
}

func (p *callStubProvider) Name() string { return p.name }

func (p *callStubProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: "USER: remembers stuff"}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 2, Total: 7}}
	}()
	return ch, nil
}

// TestProviderSummarizer_MemoryPurposeBackground proves the memory
// autosave path (providerSummarizer feeds AutoSaver.StoreSummary,
// the incremental background saver, and the startup raw-memory
// summarization) labels its model calls "memory" + background —
// these calls used to run entirely outside the recorder.
func TestProviderSummarizer_MemoryPurposeBackground(t *testing.T) {
	rec := stats.NewMemory()
	// Same wiring as main.go: the provider is metered with a non-
	// memory default purpose; providerSummarizer must override it.
	prov := llm.Metered(&callStubProvider{name: "small"}, "test", llm.PurposeDraft, statsCallSink(rec))
	fn := providerSummarizer(prov)
	out, err := fn(context.Background(), "summarize this fragment")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if out == "" {
		t.Fatal("empty summary")
	}
	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Purpose != llm.PurposeMemory {
		t.Errorf("Purpose = %q, want %q", c.Purpose, llm.PurposeMemory)
	}
	if !c.Background {
		t.Error("Background = false, want true (autosave is background work)")
	}
	if c.TokensIn != 5 || c.TokensOut != 2 {
		t.Errorf("tokens = %d/%d, want 5/2", c.TokensIn, c.TokensOut)
	}
	if c.Model != "small" || c.Provider != "test" {
		t.Errorf("identity = %q/%q", c.Model, c.Provider)
	}
}

// TestStatsCallSink_Conversion proves the llm.CallStat →
// stats.Call field mapping, including the µs conversions.
func TestStatsCallSink_Conversion(t *testing.T) {
	rec := stats.NewMemory()
	sink := statsCallSink(rec)
	sink(llm.CallStat{
		Purpose:    "navigator",
		Provider:   "openai",
		Model:      "m",
		Background: false,
		Canceled:   true,
		Failed:     true,
		TTFT:       1500 * time.Microsecond,
		Duration:   2 * time.Millisecond,
		TokensIn:   10,
		TokensOut:  20,
	})
	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Purpose != "navigator" || c.Provider != "openai" || c.Model != "m" {
		t.Errorf("identity = %+v", c)
	}
	if c.TTFTUs != 1500 || c.DurationUs != 2000 {
		t.Errorf("timing = ttft %dµs dur %dµs, want 1500/2000", c.TTFTUs, c.DurationUs)
	}
	if !c.Canceled || !c.Failed || c.Background {
		t.Errorf("flags = %+v", c)
	}
	if c.TokensIn != 10 || c.TokensOut != 20 {
		t.Errorf("tokens = %d/%d", c.TokensIn, c.TokensOut)
	}
	// Nil recorder disables metering entirely.
	if statsCallSink(nil) != nil {
		t.Error("statsCallSink(nil) must be nil (disables llm.Metered)")
	}
}
