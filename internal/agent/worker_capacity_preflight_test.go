package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestTaskFullCapacitySkipsPreparation(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, status := range []string{"created", "running"} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, status), func(t *testing.T) {
				var providers []llm.Provider
				at := newModelTestTool(t, &providers)
				at.ParentLoop = &Loop{thinTools: thin}
				at.Workers.maxActive = 1
				existing := at.Workers.Add("general", "existing", nil)
				existing.setState(func(w *Worker) { w.Status = status })
				preflight, pings := 0, 0
				at.Preflight = func() string { preflight++; return "repo context" }
				at.WorkerProvider = &stubReplyProvider{name: "selected-worker", reply: "done"}
				at.WorkerPing = func(context.Context) error { pings++; return nil }
				result, err := at.execute(context.Background(), json.RawMessage("{\"prompt\":\"inspect\"}"))
				if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "worker limit reached") {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				if preflight != 0 || pings != 0 || len(providers) != 0 {
					t.Errorf("rejected task performed preflight=%d pings=%d loop setups=%d", preflight, pings, len(providers))
				}
				if len(at.Workers.List()) != 1 || existing.Snapshot().Status != status || at.workerProbed {
					t.Error("rejected task changed workers or cached a probe")
				}
			})
		}
	}
}

func TestTaskCapacityReopensAfterWorkerFinishes(t *testing.T) {
	for _, status := range []string{"done", "failed", "stopped"} {
		t.Run(status, func(t *testing.T) {
			var providers []llm.Provider
			at := newModelTestTool(t, &providers)
			at.Workers.maxActive = 1
			existing := at.Workers.Add("general", "existing", nil)
			preflight, pings := 0, 0
			at.Preflight = func() string { preflight++; return "repo context" }
			at.WorkerProvider = &stubReplyProvider{name: "selected-worker", reply: "done"}
			at.WorkerPing = func(context.Context) error { pings++; return nil }
			args := json.RawMessage("{\"prompt\":\"inspect\"}")
			result, _ := at.execute(context.Background(), args)
			if result.Err == nil {
				t.Fatal("fixture did not fill capacity")
			}
			if preflight != 0 || pings != 0 || len(providers) != 0 {
				t.Fatal("blocked task prepared a worker")
			}
			existing.setState(func(w *Worker) { w.Status = status })
			result, err := at.execute(context.Background(), args)
			if err != nil || result.Err != nil {
				t.Fatalf("slot did not reopen: %v / %v", err, result.Err)
			}
			if preflight != 1 || pings != 1 || len(providers) != 1 || providers[0] != at.WorkerProvider {
				t.Fatalf("resumed preparation: preflight=%d pings=%d providers=%v", preflight, pings, providers)
			}
		})
	}
}

// Another task may fill capacity after the early check. Registration must still
// enforce the cap atomically rather than treating the earlier observation as a reservation.
func TestTaskCapacityIsRecheckedAfterPreparation(t *testing.T) {
	var providers []llm.Provider
	at := newModelTestTool(t, &providers)
	at.Workers.maxActive = 1
	at.Preflight = func() string { at.Workers.Add("general", "competing task", nil); return "repo context" }
	result, err := at.execute(context.Background(), json.RawMessage("{\"prompt\":\"inspect\"}"))
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "worker limit reached") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(at.Workers.List()) != 1 || at.Workers.Counts().Total != 1 {
		t.Fatal("capacity check admitted a second worker")
	}
	if len(providers) != 1 {
		t.Fatal("fixture did not reach child preparation")
	}
}

func BenchmarkTaskFullCapacity(b *testing.B) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, err := NewAgentTool(reg, nil, workerSchemaFixture(b), &stubReplyProvider{name: "fixture", reply: "done"}, nil, NewLoop)
	if err != nil {
		b.Fatal(err)
	}
	at.Workers.maxActive = 1
	at.Workers.Add("general", "occupying slot", nil)
	args := json.RawMessage("{\"agent\":\"code\",\"prompt\":\"inspect\"}")
	b.ReportAllocs()
	for b.Loop() {
		result, err := at.execute(context.Background(), args)
		if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "worker limit reached") {
			b.Fatalf("%v / %v", err, result.Err)
		}
	}
}

func TestTaskCapacityNilAndUnlimitedRegistry(t *testing.T) {
	for _, nilRegistry := range []bool{false, true} {
		t.Run(fmt.Sprintf("nil=%v", nilRegistry), func(t *testing.T) {
			var providers []llm.Provider
			at := newModelTestTool(t, &providers)
			if nilRegistry {
				at.Workers = nil
			} else {
				at.Workers.maxActive = 0
				at.Workers.Add("general", "existing", nil)
			}
			result, err := at.execute(context.Background(), json.RawMessage("{\"prompt\":\"inspect\"}"))
			if err != nil || result.Err != nil || len(providers) != 1 {
				t.Fatalf("task: %v / %v; loops=%d", err, result.Err, len(providers))
			}
		})
	}
}

func BenchmarkWorkerAvailableCapacityCheck(b *testing.B) {
	r := NewWorkerRegistry()
	r.retention = 0
	r.maxActive = 6
	for i := 0; i < 20; i++ {
		w := r.Add("general", "finished", nil)
		w.setState(func(w *Worker) { w.Status = "done" })
	}
	for i := 0; i < 5; i++ {
		r.Add("general", "active", nil)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := r.checkActiveLimit(); err != nil {
			b.Fatal(err)
		}
	}
}
