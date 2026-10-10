package app

import (
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

func TestContextWindowBundleUsesScopedRuntimeAndRebinds(t *testing.T) {
	const model = "bundle-runtime-shared-model"
	mainCfg := config.Config{Provider: config.ProviderEcho, Model: model, APIKey: t.Name() + "-main"}
	worker := config.ProviderConf{Name: "worker", Type: config.ProviderEcho, Model: model,
		BaseURL: "http://127.0.0.1:43856/v1", APIKey: t.Name() + "-worker"}
	nextCfg := config.Config{Provider: config.ProviderEcho, Model: model,
		BaseURL: "http://127.0.0.1:43857/v1", APIKey: t.Name() + "-next"}
	for _, item := range []struct {
		base, key string
		window    int
	}{{mainCfg.BaseURL, mainCfg.APIKey, 16000}, {worker.BaseURL, worker.APIKey, 64000}, {nextCfg.BaseURL, nextCfg.APIKey, 32768}} {
		llm.RememberProviderRuntimeContexts(item.base, item.key, []llm.ModelInfo{{ID: model, RuntimeContextLength: item.window}})
		t.Cleanup(func() { llm.RememberProviderRuntimeContexts(item.base, item.key, nil) })
	}
	tc := config.TomlConfig{Providers: []config.ProviderConf{
		{Name: "main", Type: mainCfg.Provider, Model: model, APIKey: mainCfg.APIKey}, worker,
	}}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, ContextLength: 262144})
	// An empty initial endpoint skips the old background probe. These callbacks
	// exercise only the seeded metadata cache and never contact a backend.
	bundle := wireContextWindows(t.TempDir(), mainCfg, tc, caps, nil, tools.NewRegistry(), nil, nil)
	for _, item := range []struct {
		provider string
		window   int
	}{{"main", 16000}, {"worker", 64000}} {
		got := bundle.ScopedContextWindowFor(item.provider, model)
		if got.Tokens != item.window || got.Source != "provider-runtime" {
			t.Fatalf("%s: resolution=%+v, want window %d", item.provider, got, item.window)
		}
	}
	if err := bundle.ModelContexts.Set("worker", model, 200000); err != nil {
		t.Fatal(err)
	}
	if got := bundle.ScopedContextWindowFor("worker", model); got != (agent.ContextWindowResolution{Tokens: 64000, Source: "provider-runtime"}) {
		t.Fatalf("worker manual budget escaped its own runtime: %+v", got)
	}
	if err := bundle.ModelContexts.Set("main", model, 8000); err != nil {
		t.Fatal(err)
	}
	if got := bundle.ScopedContextWindowFor("main", model); got != (agent.ContextWindowResolution{Tokens: 8000, Source: "model-override"}) {
		t.Fatalf("smaller main manual budget changed: %+v", got)
	}
	bundle.RebindConfig("new-profile", nextCfg)
	if got := bundle.ContextWindowFor(model); got.Tokens != 32768 || got.Source != "provider-runtime" || bundle.WindowFor(model) != 32768 {
		t.Fatalf("unscoped/legacy callbacks kept the old endpoint: %+v", got)
	}
	if got := bundle.ScopedContextWindowFor("new-profile", model); got.Tokens != 32768 || got.Source != "provider-runtime" {
		t.Fatalf("scoped callback kept the old endpoint: %+v", got)
	}
	if got := bundle.ScopedContextWindowFor("worker", model); got.Tokens != 64000 {
		t.Fatalf("rebind moved the worker to the coordinator endpoint: %+v", got)
	}
	if got := bundle.ScopedContextWindowFor("unknown-profile", model); got.Tokens != 262144 || got.Source != "catalog" {
		t.Fatalf("unknown provider borrowed loaded capacity: %+v", got)
	}
	if got := bundle.ScopedContextWindowFor("unknown-profile", "unknown-model"); got.Tokens != agent.DefaultContextWindow() || got.Source != "fallback" {
		t.Fatalf("unknown worker can fall through to the parent callback: %+v", got)
	}
}
