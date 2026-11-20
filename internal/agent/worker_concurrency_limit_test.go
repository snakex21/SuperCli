package agent

import (
	"strings"
	"testing"
)

// 1.0.37: the active-worker cap is a setting (max_parallel_agents):
// nil = default 6, 0 = unlimited, plus optional per-provider caps.
func TestWorkerConcurrencyLimitsConfigurable(t *testing.T) {
	t.Setenv("SUPERCLI_MAX_ACTIVE_WORKERS", "")
	reg := NewWorkerRegistry()
	if max, _ := reg.ConcurrencyLimits(); max != DefaultMaxActiveWorkers {
		t.Fatalf("default cap = %d", max)
	}
	two := 2
	reg.SetConcurrencyLimits(&two, nil)
	for i := 0; i < 2; i++ {
		if _, err := reg.TryAdd("code", "x", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.TryAdd("code", "x", nil); err == nil || !strings.Contains(err.Error(), "max_parallel_agents") {
		t.Fatalf("third worker over a cap of 2 = %v", err)
	}
	zero := 0
	reg.SetConcurrencyLimits(&zero, nil)
	for i := 0; i < 10; i++ {
		if _, err := reg.TryAdd("code", "x", nil); err != nil {
			t.Fatalf("unlimited refused worker %d: %v", i, err)
		}
	}
	reg.SetConcurrencyLimits(nil, nil)
	if max, _ := reg.ConcurrencyLimits(); max != DefaultMaxActiveWorkers {
		t.Fatalf("reset to default = %d", max)
	}
}

func TestWorkerConcurrencyPerProvider(t *testing.T) {
	reg := NewWorkerRegistry()
	zero := 0
	reg.SetConcurrencyLimits(&zero, map[string]int{"LMStudio": 1, "cloud": 0})
	if _, err := reg.TryAddFor("lmstudio", "code", "a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.TryAddFor("LMSTUDIO", "code", "b", nil); err == nil || !strings.Contains(err.Error(), "lmstudio") {
		t.Fatalf("second lmstudio worker = %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := reg.TryAddFor("cloud", "code", "c", nil); err != nil {
			t.Fatalf("cloud (no own cap) refused: %v", err)
		}
	}
	// A finished worker frees its provider slot.
	for _, w := range reg.List() {
		if w.provider == "lmstudio" {
			w.setState(func(w *Worker) { w.Status = "done" })
		}
	}
	if _, err := reg.TryAddFor("lmstudio", "code", "d", nil); err != nil {
		t.Fatalf("lmstudio slot not freed: %v", err)
	}
}
