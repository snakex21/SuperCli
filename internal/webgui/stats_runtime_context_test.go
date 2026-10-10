package webgui

import (
	"context"
	"fmt"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestStatsAndLoopUseRuntimeWithoutRewritingHistoricalWindow(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	eng.cfg.Model, eng.cfg.BaseURL, eng.cfg.APIKey = "gui-loaded-runtime-model", "http://127.0.0.1:43858/v1", t.Name()
	eng.prov, _ = llm.NewEcho(eng.cfg.Model)
	eng.caps = llm.NewCapabilityRegistry()
	eng.caps.Register(llm.ModelInfo{ID: eng.cfg.Model, ContextLength: 262144})
	llm.RememberProviderRuntimeContexts(eng.cfg.BaseURL, eng.cfg.APIKey, []llm.ModelInfo{{ID: eng.cfg.Model, RuntimeContextLength: 100608}})
	t.Cleanup(func() { llm.RememberProviderRuntimeContexts(eng.cfg.BaseURL, eng.cfg.APIKey, nil) })
	writeDataConfig(t, dir, "context_window = 1000000\n")
	ctx := context.Background()
	fresh, err := eng.stats(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ActiveContext.Window != 100608 || fresh.Context.Window != 100608 || fresh.Context.HasSnapshot || fresh.ActiveContext.WindowSource != "provider-runtime" {
		t.Fatalf("new chat did not use loaded capacity: %+v", fresh)
	}
	id := eng.usageIdentity(eng.cfg, "model")
	u := usageRecordFromIdentity(id)
	u.SessionID, u.Input, u.Output = sess.ID, 1000, 100
	u.ContextWindow, u.ContextUser = 1010000, 202000
	if err := store.AppendUsage(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		manual, window int
		source         string
	}{{0, 100608, "provider-runtime"}, {200000, 100608, "provider-runtime"}, {64000, 64000, "model-override"}, {0, 100608, "provider-runtime"}} {
		if tt.manual > 0 {
			if err := eng.modelContexts.Set(fresh.ActiveContext.Provider, eng.cfg.Model, tt.manual); err != nil {
				t.Fatal(err)
			}
		} else if _, err := eng.modelContexts.Remove(fresh.ActiveContext.Provider, eng.cfg.Model); err != nil {
			t.Fatal(err)
		}
		got, err := eng.stats(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext.Window != tt.window || got.ActiveContext.WindowSource != tt.source || got.ActiveContext.CompactThreshold != agent.AutoCompactThreshold(tt.window) {
			t.Fatalf("manual=%d, active context=%+v", tt.manual, got.ActiveContext)
		}
		if got.Context.Window != 1010000 || got.Context.Percent != 20 || got.Context.EstimatedUsed != 202000 || !got.Context.HasSnapshot || got.Tokens.Input != 1000 || got.Tokens.Output != 100 {
			t.Fatalf("active runtime rewrote the historical request: %+v", got)
		}
		loop, err := eng.newLoop()
		if err != nil {
			t.Fatal(err)
		}
		report := loop.ContextReport()
		if report.Window != tt.window || report.WindowSource != tt.source || report.CompactThreshold != got.ActiveContext.CompactThreshold {
			t.Fatalf("loop and active GUI limit disagree: report=%+v, active=%+v", report, got.ActiveContext)
		}
	}
}

func TestGUIWorkerRuntimeUsesItsOwnEndpointAndCredential(t *testing.T) {
	const model = "gui-worker-shared-runtime-model"
	main := config.Config{Provider: config.ProviderEcho, Model: model, BaseURL: "http://127.0.0.1:43859/v1", APIKey: t.Name() + "-main"}
	worker := config.ProviderConf{Name: "worker", Type: config.ProviderEcho, Model: model,
		BaseURL: "http://127.0.0.1:43860/v1", APIKey: t.Name() + "-worker"}
	for _, item := range []struct {
		base, key string
		window    int
	}{{main.BaseURL, main.APIKey, 8192}, {worker.BaseURL, worker.APIKey, 64000}} {
		llm.RememberProviderRuntimeContexts(item.base, item.key, []llm.ModelInfo{{ID: model, RuntimeContextLength: item.window}})
		t.Cleanup(func() { llm.RememberProviderRuntimeContexts(item.base, item.key, nil) })
	}
	configured := []config.ProviderConf{{Name: "main", Type: main.Provider, Model: model, BaseURL: main.BaseURL, APIKey: main.APIKey}, worker}
	if provider := runtimeProviderForConfig(main, nil, configured); provider != "main" {
		t.Fatalf("captured active provider=%q", provider)
	}
	configForProvider := contextWindowConfigForProvider(main, "main", configured)
	for _, tt := range []struct {
		provider string
		want     int
	}{{"main", 8192}, {"worker", 64000}, {"unknown", 0}} {
		cfg := configForProvider(tt.provider)
		got := agent.ResolveContextWindowWithRuntime(model, 0, 0, nil, nil, cfg.BaseURL, cfg.APIKey)
		if got.Tokens != tt.want {
			t.Fatalf("provider=%s borrowed another endpoint's runtime: %+v", tt.provider, got)
		}
	}
	wrongCredential := configForProvider("worker")
	wrongCredential.APIKey = main.APIKey
	if got := agent.ResolveContextWindowWithRuntime(model, 0, 0, nil, nil, wrongCredential.BaseURL, wrongCredential.APIKey); got.Tokens != 0 {
		t.Fatalf("worker runtime leaked across credentials: %+v", got)
	}
}

func TestLegacyUsageWithoutWindowResolvesConfiguredRuntimeCredential(t *testing.T) {
	eng, _, _, dir := statsFixture(t)
	base, key, model := "http://127.0.0.1:43861/v1", t.Name(), "gui-legacy-runtime-model"
	writeDataConfig(t, dir, fmt.Sprintf(`[[providers]]
name = "previous-profile"
type = "echo"
base_url = %q
api_key = %q
model = %q
`, base, key, model))
	llm.RememberProviderRuntimeContexts(base, key, []llm.ModelInfo{{ID: model, RuntimeContextLength: 32768}})
	t.Cleanup(func() { llm.RememberProviderRuntimeContexts(base, key, nil) })
	eng.caps = llm.NewCapabilityRegistry()
	eng.caps.Register(llm.ModelInfo{ID: model, ContextLength: 8192})
	u := session.UsageRecord{Provider: "previous-profile", ProviderType: config.ProviderEcho, EndpointHost: endpointHost(base),
		Model: model, Input: 1000, Output: 100, ContextUser: 4096, Source: "model"}
	got := eng.statsContextFromUsage(u, eng.usageIdentity(eng.cfg, "model"))
	if got.Window != 32768 || got.WindowSource != "provider-runtime" || got.EstimatedUsed != 4096 || !got.HasSnapshot {
		t.Fatalf("legacy row did not resolve its own connection credential: %+v", got)
	}
}
