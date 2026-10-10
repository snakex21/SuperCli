package agent

import (
	"testing"

	"supercli/internal/llm"
)

func rememberWindowRuntime(t *testing.T, base, key, model string, tokens int) {
	t.Helper()
	llm.RememberProviderRuntimeContexts(base, key, []llm.ModelInfo{{ID: model, RuntimeContextLength: tokens}})
	t.Cleanup(func() { llm.RememberProviderRuntimeContexts(base, key, nil) })
}

func TestResolveContextWindowWithRuntimeUsesLoadedCapacity(t *testing.T) {
	const model = "runtime-window-model"
	cases := []struct {
		name                         string
		configured, catalog, runtime int
		want                         ContextWindowResolution
	}{
		{"smaller loaded instance", 0, 262144, 100608, ContextWindowResolution{100608, "provider-runtime"}},
		{"tiny loaded instance", 0, 128000, 4096, ContextWindowResolution{4096, "provider-runtime"}},
		{"runtime above catalog", 0, 8192, 32768, ContextWindowResolution{32768, "provider-runtime"}},
		{"smaller explicit budget", 16000, 8192, 32768, ContextWindowResolution{16000, "config"}},
		{"explicit budget bounded", 1000000, 262144, 100608, ContextWindowResolution{100608, "provider-runtime"}},
		{"equal explicit budget", 32768, 8192, 32768, ContextWindowResolution{32768, "config"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			base, key := "http://127.0.0.1:43851/v1", t.Name()
			rememberWindowRuntime(t, base, key, model, tt.runtime)
			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: model, ContextLength: tt.catalog})
			got := ResolveContextWindowWithRuntime(model, tt.configured, 500000, caps, nil, base, key)
			if got != tt.want {
				t.Fatalf("resolution=%+v, want %+v", got, tt.want)
			}
			loop := &Loop{modelID: model, contextWindowFor: func(string) ContextWindowResolution { return got }}
			if loop.window() != tt.want.Tokens || AutoCompactThreshold(loop.window()) != AutoCompactThreshold(tt.want.Tokens) {
				t.Fatalf("loop retained a different limit: %+v", loop.windowResolution())
			}
		})
	}
}

func TestResolveContextWindowWithoutRuntimePreservesCascade(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "runtime-absent-model", ContextLength: 32000})
	caps.Register(llm.ModelInfo{ID: "vendor/runtime-absent-alias", ContextLength: 48000})
	cases := []struct {
		model, base                string
		configured, providerTokens int
	}{
		{"runtime-absent-model", "http://127.0.0.1:43852/v1", 1000000, 0},
		{"runtime-absent-model", "http://127.0.0.1:43852/v1", 0, 64000},
		{"runtime-absent-model", "http://127.0.0.1:43852/v1", 0, 0},
		{"runtime-absent-alias", "http://127.0.0.1:43852/v1", 0, 0},
		{"unknown-runtime-absent", "http://127.0.0.1:43852/v1", 0, 0},
		{"unknown-runtime-absent", "https://runtime-absent.invalid/v1", 1000000, 0},
		{"unknown-runtime-absent", "https://runtime-absent.invalid/v1", 0, 0},
	}
	for _, tt := range cases {
		want := ResolveContextWindow(tt.model, tt.configured, tt.providerTokens, caps, nil, tt.base)
		got := ResolveContextWindowWithRuntime(tt.model, tt.configured, tt.providerTokens, caps, nil, tt.base, t.Name())
		if got != want {
			t.Fatalf("%+v: resolution=%+v, legacy=%+v", tt, got, want)
		}
	}
}

func TestRuntimeWindowDoesNotCrossEndpointCredentialOrModel(t *testing.T) {
	const model = "runtime-scoped-window-model"
	base, key := "http://127.0.0.1:43853/v1", t.Name()
	rememberWindowRuntime(t, base, key, model, 4096)
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, ContextLength: 32000})
	for _, tt := range []struct{ base, key, model string }{
		{"http://127.0.0.1:43854/v1", key, model},
		{base, key + "-other-credential", model},
		{base, key, "vendor/" + model},
	} {
		want := ResolveContextWindow(tt.model, 0, 0, caps, nil, tt.base)
		got := ResolveContextWindowWithRuntime(tt.model, 0, 0, caps, nil, tt.base, tt.key)
		if got != want {
			t.Fatalf("runtime capacity leaked across identity %+v: %+v", tt, got)
		}
	}
}

func TestClampScopedBudgetToActualRuntimeOnly(t *testing.T) {
	base, key, model := "http://127.0.0.1:43855/v1", t.Name(), "runtime-manual-budget-model"
	rememberWindowRuntime(t, base, key, model, 32768)
	for _, tokens := range []int{16000, 32768, 200000} {
		manual := ContextWindowResolution{tokens, "model-override"}
		want := manual
		if tokens > 32768 {
			want = ContextWindowResolution{32768, "provider-runtime"}
		}
		if got := ClampContextWindowToRuntime(manual, base, key, model); got != want {
			t.Fatalf("manual=%+v, got %+v, want %+v", manual, got, want)
		}
		if got := ClampContextWindowToRuntime(manual, base, key+"-unknown", model); got != manual {
			t.Fatalf("absent runtime changed explicit budget: %+v", got)
		}
	}
}
