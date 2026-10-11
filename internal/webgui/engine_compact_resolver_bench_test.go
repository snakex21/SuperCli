package webgui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

type compactConstructionBenchProvider struct {
	model string
	calls *atomic.Int64
}

func (p *compactConstructionBenchProvider) Name() string { return p.model }

func (p *compactConstructionBenchProvider) Complete(ctx context.Context, _ []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls.Add(1)
	if len(defs) != 0 {
		panic("compaction benchmark unexpectedly received tool schemas")
	}
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: "Goal: finish universal optimization\nDone: fixture investigated\nState: keep thinking level and earlier requirements\nPending: finish current task", FinishReason: "stop", Usage: &llm.Usage{Input: 120, Output: 24}}
	close(ch)
	return ch, nil
}

// Full GUI loop creation over portable on-disk configuration and the genuine
// registry, with a deterministic factory/provider boundary. There is no model
// inference, network, artificial latency or altered reasoning configuration.
// The first-summary control includes the real CompactNow path; its construction
// is deferred, not eliminated. The same benchmark builds against frozen GUI
// sources to compare counts and latency with the original eager wiring.
func BenchmarkWebCompactConstruction(b *testing.B) {
	for _, fixture := range []struct {
		name                string
		override, summarize bool
	}{
		{"inherited_control", false, false},
		{"unused_override", true, false},
		{"first_summary_control", true, true},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			data, home := b.TempDir(), b.TempDir()
			body := "orchestrator = false\npreflight_repo = false\ncontext_window = 100000\n"
			if fixture.override {
				body += "compact_model = \"summary-fixture\"\n"
			}
			if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte(body), 0600); err != nil {
				b.Fatal(err)
			}
			e, err := NewEngine(echoConfig(), home, data)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = e.Close() })
			var builds, completions atomic.Int64
			e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
				builds.Add(1)
				return &compactConstructionBenchProvider{model: cfg.Model, calls: &completions}, nil
			}, data, e.caps)
			var initial []llm.Message
			if fixture.summarize {
				initial = fatTurns(4, 2000)
			}
			b.ReportAllocs()
			for b.Loop() {
				loop, err := e.newLoopWithSession(initial, nil)
				if err != nil {
					b.Fatal(err)
				}
				if len(loop.Messages) == 0 {
					b.Fatal("loop lost its prompt")
				}
				if fixture.summarize {
					if _, err := loop.CompactNow(context.Background()); err != nil {
						b.Fatal(err)
					}
				}
			}
			wantCalls := int64(0)
			if fixture.summarize {
				wantCalls = int64(b.N)
			}
			if completions.Load() != wantCalls {
				b.Fatalf("helper calls=%d, want %d", completions.Load(), wantCalls)
			}
			if fixture.summarize && builds.Load() != int64(b.N) {
				b.Fatal("actual first summary lost its configured provider")
			}
			b.ReportMetric(float64(builds.Load())/float64(b.N), "backend-builds/loop")
			b.ReportMetric(float64(completions.Load())/float64(b.N), "model-calls/loop")
		})
	}
}
