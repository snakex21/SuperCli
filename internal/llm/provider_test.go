package llm

import (
	"context"
	"errors"
	"testing"
)

// mockProvider implements Provider for testing.
type mockProvider struct {
	name     string
	complete func(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error)
}

func (m *mockProvider) Name() string                                         { return m.name }
func (m *mockProvider) Complete(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
	return m.complete(ctx, msgs, nil)
}

func TestProvider_Name(t *testing.T) {
	p := &mockProvider{name: "test-model"}
	if p.Name() != "test-model" {
		t.Errorf("Name() = %q, want test-model", p.Name())
	}
}

func TestProvider_Complete_ReturnsChannel(t *testing.T) {
	p := &mockProvider{
		name: "mock",
		complete: func(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
			ch := make(chan Delta, 1)
			ch <- Delta{FinishReason: "stop"}
			close(ch)
			return ch, nil
		},
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	d := <-ch
	if d.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", d.FinishReason)
	}
}

func TestProvider_Complete_ErrorOnInit(t *testing.T) {
	wantErr := errors.New("config error")
	p := &mockProvider{
		name: "mock",
		complete: func(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
			return nil, wantErr
		},
	}
	_, err := p.Complete(context.Background(), nil, nil)
	if err != wantErr {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestProvider_Complete_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &mockProvider{
		name: "mock",
		complete: func(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
			return nil, ctx.Err()
		},
	}
	_, err := p.Complete(ctx, nil, nil)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestProvider_Complete_StreamErrorViaDelta(t *testing.T) {
	wantErr := errors.New("stream broken")
	p := &mockProvider{
		name: "mock",
		complete: func(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
			ch := make(chan Delta, 1)
			ch <- Delta{Err: wantErr}
			close(ch)
			return ch, nil
		},
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	d := <-ch
	if d.Err != wantErr {
		t.Errorf("delta.Err = %v, want %v", d.Err, wantErr)
	}
}
