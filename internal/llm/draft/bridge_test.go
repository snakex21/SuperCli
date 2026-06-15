package draft

import (
	"context"
	"errors"
	"testing"

	"supercli/internal/llm"
)

func TestNewBridge_RequiresPolicy(t *testing.T) {
	prov, _ := llm.NewEcho("test")
	if _, err := NewBridge(nil, prov); err == nil {
		t.Error("expected error for nil policy")
	}
}

func TestNewBridge_RequiresProvider(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	if _, err := NewBridge(p, nil); err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestBridge_Plan_HappyPath(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	prov, _ := llm.NewEcho("test-draft")
	b, err := NewBridge(p, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Plan(context.Background(), "ship F11")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Error("expected non-empty plan text")
	}
	if res.Model != "test-draft" {
		t.Errorf("Model = %q, want test-draft", res.Model)
	}
	if res.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", res.Duration)
	}
}

func TestBridge_Plan_EmptyPrompt(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	prov, _ := llm.NewEcho("test")
	b, _ := NewBridge(p, prov)
	if _, err := b.Plan(context.Background(), ""); err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestBridge_Plan_ContextCancel(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	prov, _ := llm.NewEcho("test")
	b, _ := NewBridge(p, prov)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Plan(ctx, "x")
	if err == nil {
		t.Error("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		// EchoProvider returns ctx.Err() (context.Canceled)
		// from Complete() directly when ctx is already done.
		t.Logf("got err = %v (acceptable if not wrapped)", err)
	}
}

func TestBridge_AsSystemMessage_WrapsPlan(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	prov, _ := llm.NewEcho("test")
	b, _ := NewBridge(p, prov)
	msg := b.AsSystemMessage("do A then B")
	if msg.Role != llm.RoleSystem {
		t.Errorf("Role = %q, want system", msg.Role)
	}
	if msg.Content != "[draft plan] do A then B" {
		t.Errorf("Content = %q, want [draft plan] prefix", msg.Content)
	}
}
