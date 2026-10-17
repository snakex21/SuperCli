package darwin

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

// runPoolCount runs the tool with raw args and returns how many candidate
// loops were spawned.
func runPoolCount(t *testing.T, fixed int, raw string) int32 {
	t.Helper()
	d := newTestTool(t)
	d.SetPoolSize(fixed)
	d.SetSequential(true)
	var calls int32
	d.SetLoopFactory(func(LoopConfig) (Loop, error) {
		atomic.AddInt32(&calls, 1)
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	res, err := d.Spec().Fn(context.Background(), json.RawMessage(raw))
	if err != nil || res.Err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	return atomic.LoadInt32(&calls)
}

// darwin_pool_size 1..10 overrides whatever pool_size the model passes.
func TestDarwinTool_FixedPoolSizeOverridesModel(t *testing.T) {
	if got := runPoolCount(t, 2, `{"prompt":"x","pool_size":5,"no_worktree":true,"judge":"heuristic"}`); got != 2 {
		t.Fatalf("fixed pool 2 with model pool_size 5 spawned %d", got)
	}
	// Even an out-of-range model value is ignored when the user fixed it.
	if got := runPoolCount(t, 4, `{"prompt":"x","pool_size":50,"no_worktree":true,"judge":"heuristic"}`); got != 4 {
		t.Fatalf("fixed pool 4 with model pool_size 50 spawned %d", got)
	}
}

// darwin_pool_size 0 keeps today's behaviour: the model decides, default 3.
func TestDarwinTool_AutoPoolSizeLetsModelDecide(t *testing.T) {
	if got := runPoolCount(t, 0, `{"prompt":"x","pool_size":2,"no_worktree":true,"judge":"heuristic"}`); got != 2 {
		t.Fatalf("auto with model pool_size 2 spawned %d", got)
	}
	if got := runPoolCount(t, 0, `{"prompt":"x","no_worktree":true,"judge":"heuristic"}`); got != 3 {
		t.Fatalf("auto default spawned %d, want 3", got)
	}
}

func TestDarwinTool_SetPoolSizeClamps(t *testing.T) {
	d := newTestTool(t)
	d.SetPoolSize(42)
	if d.poolSize != maxPoolSize {
		t.Fatalf("poolSize = %d, want %d", d.poolSize, maxPoolSize)
	}
	d.SetPoolSize(-3)
	if d.poolSize != 0 {
		t.Fatalf("poolSize = %d, want 0", d.poolSize)
	}
}
