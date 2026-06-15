package main

import (
	"context"
	"testing"

	"supercli/internal/account/tier"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// draftWiringTestProvider is a minimal verifier provider
// whose Name() is the "main" model id.
type draftWiringTestProvider struct{ name string }

func (p draftWiringTestProvider) Name() string { return p.name }

func (p draftWiringTestProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta)
	close(out)
	return out, nil
}

// regWithCandidate builds a capability registry that DOES
// contain a cheap tool-using small-tier model. Under the
// old auto-pick behavior this would have been engaged as
// the draft model. The opt-in behavior must ignore it.
func regWithCandidate(verifier string) *llm.CapabilityRegistry {
	r := llm.NewCapabilityRegistry()
	r.Register(llm.ModelInfo{ID: verifier, ToolUse: true, InputCost: 1.0, OutputCost: 1.0})
	r.Register(llm.ModelInfo{ID: "llama-3.1-8b", ToolUse: true, InputCost: 0.01, OutputCost: 0.01})
	return r
}

// TestBuildDraftWiring_NoModel_NoDraft asserts the opt-in
// rule: when no draft model is explicitly configured,
// buildDraftWiring returns (nil, nil) even though a
// non-off mode is requested and a candidate model exists
// in the registry. This is the regression guard for the
// "auto-engaged llama-3.1-8b" bug.
func TestBuildDraftWiring_NoModel_NoDraft(t *testing.T) {
	verifier := draftWiringTestProvider{name: "qwen3.5-9b"}
	caps := regWithCandidate(verifier.name)
	cfg := config.Config{Provider: config.ProviderEcho}

	for _, mode := range []string{"critical", "balanced", "always"} {
		policy, prov := buildDraftWiring(mode, "", verifier, caps, cfg, []tier.Rule(nil))
		if policy != nil || prov != nil {
			t.Errorf("mode=%q with empty draft model: got (policy=%v, prov=%v), want (nil, nil) — F11 must stay opt-in", mode, policy, prov)
		}
	}
}

// TestBuildDraftWiring_OffMode_NoDraft asserts that the
// default off mode disables F11 even if a draft model is
// explicitly set.
func TestBuildDraftWiring_OffMode_NoDraft(t *testing.T) {
	verifier := draftWiringTestProvider{name: "qwen3.5-9b"}
	caps := regWithCandidate(verifier.name)
	cfg := config.Config{Provider: config.ProviderEcho}

	policy, prov := buildDraftWiring("off", "llama-3.1-8b", verifier, caps, cfg, []tier.Rule(nil))
	if policy != nil || prov != nil {
		t.Errorf("mode=off: got (policy=%v, prov=%v), want (nil, nil)", policy, prov)
	}
}

// TestBuildDraftWiring_ExplicitModel_Engages asserts that
// the opt-in path still works: an explicit draft model +
// non-off mode engages F11.
func TestBuildDraftWiring_ExplicitModel_Engages(t *testing.T) {
	verifier := draftWiringTestProvider{name: "qwen3.5-9b"}
	caps := regWithCandidate(verifier.name)
	cfg := config.Config{Provider: config.ProviderEcho}

	policy, prov := buildDraftWiring("balanced", "llama-3.1-8b", verifier, caps, cfg, []tier.Rule(nil))
	if policy == nil || prov == nil {
		t.Fatalf("explicit draft model: got (policy=%v, prov=%v), want both non-nil", policy, prov)
	}
	if policy.Model != "llama-3.1-8b" {
		t.Errorf("policy.Model = %q, want llama-3.1-8b", policy.Model)
	}
}
