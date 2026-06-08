package agent

import (
	"context"
	"errors"
	"testing"
)

func TestSubAgent_Validate(t *testing.T) {
	cases := []struct {
		name string
		sa   SubAgent
		ok   bool
	}{
		{"ok", SubAgent{Name: "explore", Description: "search"}, true},
		{"empty_name", SubAgent{Description: "x"}, false},
		{"empty_desc", SubAgent{Name: "x"}, false},
		{"negative_steps", SubAgent{Name: "x", Description: "y", MaxSteps: -1}, false},
	}
	for _, c := range cases {
		err := c.sa.Validate()
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestSubAgentRegistry_RegisterGet(t *testing.T) {
	r := NewSubAgentRegistry()
	err := r.Register(SubAgent{Name: "explore", Description: "search the codebase"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("explore")
	if !ok {
		t.Fatal("Get returned not found")
	}
	if got.Description != "search the codebase" {
		t.Errorf("Description = %q", got.Description)
	}
}

func TestSubAgentRegistry_RejectsDuplicate(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "explore", Description: "d1"})
	err := r.Register(SubAgent{Name: "explore", Description: "d2"})
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestSubAgentRegistry_Names(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "a", Description: "1"})
	r.MustRegister(SubAgent{Name: "b", Description: "2"})
	names := r.Names()
	if len(names) != 2 {
		t.Errorf("len = %d", len(names))
	}
}

func TestSubAgentRegistry_Len(t *testing.T) {
	r := NewSubAgentRegistry()
	if r.Len() != 0 {
		t.Errorf("Len = %d, want 0", r.Len())
	}
	r.MustRegister(SubAgent{Name: "a", Description: "1"})
	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1", r.Len())
	}
}

func TestSubAgentRegistry_Run_UnknownName(t *testing.T) {
	r := NewSubAgentRegistry()
	_, err := r.Run(context.Background(), func(ctx context.Context, s SubAgent, p string) (string, error) {
		return "ok", nil
	}, "missing", "hi")
	if err == nil {
		t.Fatal("expected error for unknown sub-agent")
	}
}

func TestSubAgentRegistry_Run_NilRunner(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "x", Description: "y"})
	_, err := r.Run(context.Background(), nil, "x", "hi")
	if err == nil {
		t.Fatal("expected error for nil runner")
	}
}

func TestSubAgentRegistry_Run_DispatchesToRunner(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "explore", Description: "search"})
	called := false
	out, err := r.Run(context.Background(), func(ctx context.Context, s SubAgent, p string) (string, error) {
		called = true
		if s.Name != "explore" {
			t.Errorf("spec.Name = %q", s.Name)
		}
		if p != "find usages of X" {
			t.Errorf("prompt = %q", p)
		}
		return "answer", nil
	}, "explore", "find usages of X")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "answer" {
		t.Errorf("out = %q", out)
	}
	if !called {
		t.Errorf("runner not called")
	}
}

func TestSubAgentRegistry_Run_RunnerError(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "x", Description: "y"})
	_, err := r.Run(context.Background(), func(ctx context.Context, s SubAgent, p string) (string, error) {
		return "", errors.New("boom")
	}, "x", "hi")
	if err == nil {
		t.Fatal("expected error from runner")
	}
}

func TestSubAgentRegistry_Concurrent(t *testing.T) {
	r := NewSubAgentRegistry()
	r.MustRegister(SubAgent{Name: "x", Description: "y"})

	// Run 5 concurrent runs to confirm RWMutex protects access.
	const n = 5
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := r.Run(context.Background(), func(ctx context.Context, s SubAgent, p string) (string, error) {
				return "ok", nil
			}, "x", "p")
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("run %d: %v", i, err)
		}
	}
}
