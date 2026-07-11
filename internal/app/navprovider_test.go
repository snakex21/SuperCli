package app

// Tests for resolveNavigatorProvider — the "which small provider does
// the navigator classify on" fallback chain. Zero new knobs: it only
// reuses providers the user already configured (task_model worker
// first, draft provider second, else nil = main provider).

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

type namedStubProvider struct{ name string }

func (p namedStubProvider) Name() string { return p.name }
func (p namedStubProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta)
	close(ch)
	return ch, nil
}

func TestResolveNavigatorProvider_PrefersTaskWorker(t *testing.T) {
	tw := namedStubProvider{name: "worker"}
	dr := namedStubProvider{name: "draft"}
	got := resolveNavigatorProvider(tw, dr)
	if got == nil || got.Name() != "worker" {
		t.Fatalf("got %v, want task_model worker provider", got)
	}
}

func TestResolveNavigatorProvider_FallsBackToDraft(t *testing.T) {
	dr := namedStubProvider{name: "draft-small"}
	got := resolveNavigatorProvider(nil, dr)
	if got == nil || got.Name() != "draft-small" {
		t.Fatalf("got %v, want draft provider", got)
	}
}

func TestResolveNavigatorProvider_SkipsEchoDraft(t *testing.T) {
	if got := resolveNavigatorProvider(nil, namedStubProvider{name: "Echo-Stub"}); got != nil {
		t.Fatalf("got %v, want nil (echo draft stubs must not classify routes)", got)
	}
}

func TestResolveNavigatorProvider_NoneConfigured(t *testing.T) {
	if got := resolveNavigatorProvider(nil, nil); got != nil {
		t.Fatalf("got %v, want nil (navigator stays on the main provider)", got)
	}
}
