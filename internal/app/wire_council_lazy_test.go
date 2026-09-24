package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

func TestConsultCouncilLazyStartupAndConcurrentUse(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	for i := range 3 {
		caps.Register(llm.ModelInfo{ID: fmt.Sprintf("sample-%d", i), ToolUse: true, Provider: "test", InputCost: float64(i + 1)})
	}
	judge, _ := llm.NewEcho("judge")
	var builds, calls atomic.Int32
	f := factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds.Add(1)
		return llm.NewEcho(cfg.Model)
	}, "", caps, func(llm.CallStat) { calls.Add(1) })
	c := newConsultCouncil(3, judge, caps, config.Config{Provider: config.ProviderOpenAI, Model: "judge"}, f)
	if builds.Load() != 0 || calls.Load() != 0 {
		t.Fatal("startup initialized optional models")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Consult(canceled, consult.Request{Question: "q", N: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if _, err := c.Consult(context.Background(), consult.Request{}); err == nil {
		t.Fatal("empty question accepted")
	}
	// A hand-picked roster never initializes the automatic fallback.
	if r, err := c.ConsultSelected(context.Background(), "explicit", []llm.Provider{judge}); err != nil || r.AllFailed {
		t.Fatalf("explicit: %+v %v", r, err)
	}
	if builds.Load() != 0 {
		t.Fatal("unused automatic roster was built")
	}
	const parallel = 8
	var wg sync.WaitGroup
	for range parallel {
		wg.Go(func() {
			r, err := c.Consult(context.Background(), consult.Request{Question: "q", N: 1})
			if err != nil || len(r.Candidates) != 1 || r.AllFailed {
				t.Errorf("consult: %+v %v", r, err)
			}
		})
	}
	wg.Wait()
	if builds.Load() != 3 {
		t.Fatalf("built %d providers, want one pool of 3", builds.Load())
	}
	if calls.Load() != parallel {
		t.Fatalf("metered calls=%d, want %d (one per consultation)", calls.Load(), parallel)
	}
	// Construction does not mutate exported configuration during concurrent use.
	if len(c.Samples) != 0 {
		t.Fatal("lazy loading mutated Samples")
	}
}

func TestConsultCouncilLazyUnavailablePool(t *testing.T) {
	judge, _ := llm.NewEcho("judge")
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "broken", ToolUse: true, Provider: "test", InputCost: 1})
	f := factory.New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		return nil, errors.New("unavailable")
	}, "", caps)
	c := newConsultCouncil(3, judge, caps, config.Config{}, f)
	if _, err := c.Consult(context.Background(), consult.Request{Question: "q"}); err == nil {
		t.Fatal("missing pool should be reported")
	}
	if r, err := c.ConsultSelected(context.Background(), "q", []llm.Provider{judge}); err != nil || r.AllFailed {
		t.Fatalf("explicit roster stopped working: %+v %v", r, err)
	}
}

var startupCouncilBenchmark *consult.Council

// No network or artificial sleeps: this isolates the optional wiring itself.
// A real factory may additionally discover catalogs or read authentication.
func BenchmarkConsultStartup(b *testing.B) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previousLog)
	caps := llm.NewCapabilityRegistry()
	for i := range 3 {
		caps.Register(llm.ModelInfo{ID: fmt.Sprintf("sample-%d", i), ToolUse: true, Provider: "test", InputCost: float64(i + 1)})
	}
	judge, _ := llm.NewEcho("judge")
	cfg := config.Config{Provider: config.ProviderOpenAI, BaseURL: "http://127.0.0.1:1234/v1", Model: "judge"}
	for _, lazy := range []bool{false, true} {
		name := "eager"
		if lazy {
			name = "lazy"
		}
		b.Run(name, func(b *testing.B) {
			var builds int
			f := factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
				builds++
				return llm.NewEcho(cfg.Model)
			}, "", caps)
			b.ReportAllocs()
			for b.Loop() {
				if lazy {
					startupCouncilBenchmark = newConsultCouncil(3, judge, caps, cfg, f)
				} else {
					startupCouncilBenchmark = buildConsultCouncil(3, judge, caps, cfg, f)
				}
			}
			b.ReportMetric(float64(builds)/float64(b.N), "providers/start")
		})
	}
}
