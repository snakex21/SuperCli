package factory

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// TestBuild_EveryTransportComesOutMetered is the factory contract:
// whatever transport the config selects, the provider handed to the
// rest of the process is wrapped in llm.Metered (purpose labels,
// background gate, foreground preemption) — and still unwraps to the
// raw transport for capability assertions.
func TestBuild_EveryTransportComesOutMetered(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
	}{
		{"echo", config.Config{Provider: config.ProviderEcho, Model: "echo-test"}},
		{"openai", config.Config{Provider: config.ProviderOpenAI, Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1"}},
		{"responses", config.Config{Provider: config.ProviderResponses, Model: "gpt-responses", BaseURL: "https://example.test/v1", APIKey: "key"}},
		{"anthropic", config.Config{Provider: config.ProviderAnthropic, Model: "claude-x", BaseURL: "https://api.anthropic.com", APIKey: "key"}},
	}
	f := New(nil, t.TempDir(), llm.NewCapabilityRegistry())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := f.Build(tc.cfg, llm.PurposeMain)
			if err != nil {
				t.Fatalf("Build(%s): %v", tc.name, err)
			}
			if !llm.IsMetered(p) {
				t.Fatalf("Build(%s) returned an unmetered provider (%T)", tc.name, p)
			}
			if inner := llm.Unwrap(p); inner == nil || llm.IsMetered(inner) {
				t.Fatalf("Build(%s): Unwrap should expose the raw transport, got %T", tc.name, inner)
			}
		})
	}
}

// TestBuild_NeverDoubleWraps: a BuildFunc that already returns a
// metered provider (e.g. composing another factory) must not get a
// second wrapper — nesting would double-report and deadlock the
// background gate.
func TestBuild_NeverDoubleWraps(t *testing.T) {
	echo, _ := llm.NewEcho("pre-wrapped")
	wrapped := llm.Metered(echo, "echo", llm.PurposeMain, func(llm.CallStat) {})
	f := New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		return wrapped, nil
	}, "", nil)
	p, err := f.Build(config.Config{Provider: config.ProviderEcho}, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
	if p != wrapped {
		t.Fatalf("factory re-wrapped an already-metered provider: %T", p)
	}
}

// TestBuild_SinksReceiveCalls: the factory bakes the fan-out sink in,
// so a call on the built provider reports to every registered sink.
func TestBuild_SinksReceiveCalls(t *testing.T) {
	var a, b []llm.CallStat
	f := New(nil, "", nil,
		func(s llm.CallStat) { a = append(a, s) },
		nil, // nil sinks are tolerated and skipped
		func(s llm.CallStat) { b = append(b, s) },
	)
	p, err := f.Build(config.Config{Provider: config.ProviderEcho, Model: "echo-test"}, llm.PurposeDraft)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(t.Context(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("sinks got %d/%d stats, want 1/1", len(a), len(b))
	}
	if a[0].Purpose != llm.PurposeDraft {
		t.Errorf("default purpose = %q, want draft", a[0].Purpose)
	}
}
