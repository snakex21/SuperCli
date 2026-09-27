package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestWorkerInheritsContextWindowCascade(t *testing.T) {
	for _, separate := range []bool{false, true} {
		for _, route := range []string{"legacy", "new-falls-through", "source-aware", "scoped"} {
			t.Run(fmt.Sprintf("%s/separate=%t", route, separate), func(t *testing.T) {
				primary := &stubReplyProvider{name: "coordinator-model", reply: "ready"}
				worker := &stubReplyProvider{name: "worker-model", reply: "ready"}
				model := primary.Name()
				scope := "primary-connection"
				if separate {
					model, scope = worker.Name(), "worker-connection"
				}
				legacy := map[string]int{primary.Name(): 65536, worker.Name(): 32768}
				newLimit, scopedLimit := 98304, 131072
				base := newTestBaseRegistry()
				cfg := LoopConfig{
					Provider: primary, Registry: base, BaseDir: t.TempDir(), ContextProvider: "primary-connection",
					WindowFor: func(id string) int { return legacy[id] },
				}
				if route != "legacy" {
					cfg.ContextWindowFor = func(id string) ContextWindowResolution {
						if id != model {
							return ContextWindowResolution{}
						}
						if route == "new-falls-through" {
							return ContextWindowResolution{}
						}
						return ContextWindowResolution{Tokens: newLimit, Source: "provider"}
					}
				}
				if route == "scoped" {
					cfg.ScopedContextWindowFor = func(provider, id string) ContextWindowResolution {
						if provider != scope || id != model {
							return ContextWindowResolution{}
						}
						return ContextWindowResolution{Tokens: scopedLimit, Source: "model-override"}
					}
				}
				parent, err := NewLoop(cfg)
				if err != nil {
					t.Fatal(err)
				}
				specs := NewSubAgentRegistry()
				specs.MustRegister(SubAgent{Name: "inspect", Description: "inspect", SkipImplementationHint: true})
				var child *Loop
				newLoops := 0
				task, err := NewAgentTool(specs, parent, base, primary, nil, func(cfg LoopConfig) (*Loop, error) {
					newLoops++
					var err error
					child, err = NewLoop(cfg)
					return child, err
				})
				if err != nil {
					t.Fatal(err)
				}
				if separate {
					task.WorkerProvider, task.WorkerContextProvider = worker, scope
				}
				result, err := task.execute(context.Background(), json.RawMessage("{\"agent\":\"inspect\",\"prompt\":\"Report ready.\"}"))
				if err != nil || result.Err != nil {
					t.Fatalf("initial task: %v %v", err, result.Err)
				}
				expected := func() ContextWindowResolution {
					switch route {
					case "source-aware":
						return ContextWindowResolution{Tokens: newLimit, Source: "provider"}
					case "scoped":
						return ContextWindowResolution{Tokens: scopedLimit, Source: "model-override"}
					default:
						return ContextWindowResolution{Tokens: legacy[model], Source: "callback"}
					}
				}
				if got := child.windowResolution(); got != expected() {
					t.Fatalf("initial worker window=%+v, want %+v", got, expected())
				}
				// Follow-ups must use the current resolver, not a frozen copy of the
				// coordinator's resolved limit or the value at worker creation.
				legacy[model] += 4096
				newLimit += 4096
				scopedLimit += 4096
				workers := task.Workers.List()
				if len(workers) != 1 {
					t.Fatalf("workers=%d", len(workers))
				}
				args, _ := json.Marshal(map[string]string{"to": workers[0].ID, "message": "Continue and report ready."})
				result, err = NewSendMessageTool(task.Workers).execute(context.Background(), args)
				if err != nil || result.Err != nil {
					t.Fatalf("followup: %v %v", err, result.Err)
				}
				if got := child.windowResolution(); got != expected() {
					t.Fatalf("continued worker window=%+v, want %+v", got, expected())
				}
				if newLoops != 1 {
					t.Fatalf("continuation created %d loops", newLoops)
				}
			})
		}
	}
}
