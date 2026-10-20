package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

const instructionTestArgs = `{"prompt":"compare","pool_size":3,"no_worktree":true,"judge":"heuristic"}`

// Capture exactly what each child receives without constructing a real agent
// or contacting a model. Multiple children catch accidental per-child loads.
func captureInstructionSystems(d *DarwinTool) <-chan string {
	seen := make(chan string, 30)
	d.SetLoopFactory(func(cfg LoopConfig) (Loop, error) {
		seen <- cfg.System
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	return seen
}

func runInstructionTest(t *testing.T, d *DarwinTool, ctx context.Context, seen <-chan string, want string) {
	t.Helper()
	result, err := d.Spec().Fn(ctx, json.RawMessage(instructionTestArgs))
	if err != nil || result.Err != nil {
		t.Fatalf("Darwin run failed: %v, result error: %v", err, result.Err)
	}
	for i := 0; i < 3; i++ {
		select {
		case got := <-seen:
			if got != want {
				t.Errorf("child %d system = %q, want %q", i, got, want)
			}
		default:
			t.Fatalf("missing child %d", i)
		}
	}
	select {
	case got := <-seen:
		t.Fatalf("unexpected extra child system: %q", got)
	default:
	}
}

func TestDarwinUserInstructionsDirectFallbackPresetSwitchAndDisable(t *testing.T) {
	d := newTestTool(t)
	base := d.system
	seen := captureInstructionSystems(d)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "direct slash invocation")
	selected := "preset A"
	loads := 0
	d.SetUserInstructionsForRun(func(gotCtx context.Context) (string, error) {
		loads++
		if gotCtx.Value(contextKey{}) != "direct slash invocation" {
			t.Error("resolver did not receive the invocation context")
		}
		return selected, nil
	})
	for i, preset := range []string{"preset A", "preset B", ""} {
		selected = preset
		want := base
		if preset != "" {
			want += "\n\n" + preset
		}
		runInstructionTest(t, d, ctx, seen, want)
		if loads != i+1 {
			t.Fatalf("resolver loads = %d, want once per invocation (%d)", loads, i+1)
		}
		if d.system != base {
			t.Fatalf("stored base mutated to %q", d.system)
		}
	}
}

func TestDarwinUserInstructionsContextSnapshotWins(t *testing.T) {
	for _, tc := range []struct {
		name, instructions string
	}{
		{name: "preset_A", instructions: "stable preset A"},
		{name: "disabled", instructions: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := agent.WithUserInstructionsSnapshot(context.Background(), tc.instructions)
			// Delegated contexts must inherit even an explicitly empty snapshot.
			ctx = context.WithValue(ctx, struct{}{}, "delegated child")
			d := newTestTool(t)
			base := d.system
			seen := captureInstructionSystems(d)
			selected := "new preset B"
			loads := 0
			d.SetUserInstructionsForRun(func(context.Context) (string, error) {
				loads++
				return selected, errors.New("fallback must not load during an owning run")
			})
			want := base
			if tc.instructions != "" {
				want += "\n\n" + tc.instructions
			}
			for _, current := range []string{"new preset B", ""} {
				selected = current
				runInstructionTest(t, d, ctx, seen, want)
			}
			if loads != 0 {
				t.Fatalf("snapshot present but fallback loaded %d times", loads)
			}
			if d.system != base {
				t.Fatalf("stored base mutated to %q", d.system)
			}
		})
	}
}

type instructionCountingProvider struct {
	providerStub
	calls atomic.Int32
}

func (p *instructionCountingProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	return p.providerStub.Complete(ctx, msgs, defs)
}

func TestDarwinUserInstructionsFallbackErrorBeforeChildrenOrModels(t *testing.T) {
	d := newTestTool(t)
	provider := &instructionCountingProvider{}
	d.provider = provider
	d.SetJudgeProvider(provider)
	d.SetModels([]llm.Provider{provider})
	var children atomic.Int32
	d.SetLoopFactory(func(LoopConfig) (Loop, error) {
		children.Add(1)
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	loadErr := errors.New("cannot load selected preset")
	loads := 0
	d.SetUserInstructionsForRun(func(context.Context) (string, error) {
		loads++
		return "partial instructions must not be used", loadErr
	})
	// The LLM judge would call the provider if the run proceeded.
	_, err := d.Spec().Fn(context.Background(), json.RawMessage(`{"prompt":"compare","pool_size":3,"no_worktree":true,"judge":"llm"}`))
	if !errors.Is(err, loadErr) {
		t.Fatalf("error = %v, want wrapped resolver error %v", err, loadErr)
	}
	if loads != 1 || children.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("loads=%d children=%d model calls=%d; want 1, 0, 0", loads, children.Load(), provider.calls.Load())
	}
}

func TestDarwinUserInstructionsLegacySystemUnchanged(t *testing.T) {
	d := newTestTool(t)
	d.system += "\n\npreset already included by legacy caller"
	seen := captureInstructionSystems(d)
	runInstructionTest(t, d, context.Background(), seen, d.system)
	// A parent snapshot must not be injected into a legacy full system.
	ctx := agent.WithUserInstructionsSnapshot(context.Background(), "different parent preset")
	runInstructionTest(t, d, ctx, seen, d.system)
	// Explicit nil retains the same legacy behavior.
	d.SetUserInstructionsForRun(nil)
	runInstructionTest(t, d, ctx, seen, d.system)
}
