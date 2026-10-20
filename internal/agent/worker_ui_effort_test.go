package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerUIEffortMetadata(t *testing.T) {
	initial := llm.ReasoningEffort()
	defer func() { _ = llm.SetReasoningEffort(initial) }()
	if err := llm.SetReasoningEffort("low"); err != nil {
		t.Fatal(err)
	}
	model := "gpt-5-worker-ui"
	llm.SetReasoningEffortSupport(model, []string{"low", "high"})
	for _, tc := range []struct{ name, override, want string }{{"worker inherits", "", "low"}, {"advisor override", "high", "high"}, {"nearest supported", "max", "high"}} {
		t.Run(tc.name, func(t *testing.T) {
			loop := makeLoop(t, &stubProvider{name: model}, tools.NewRegistry(), "")
			w := &Worker{ID: "worker-1", Agent: "advisor", Loop: loop}
			var got WorkerProgressEvent
			w.progress = func(ev WorkerProgressEvent) { got = ev }
			ctx := llm.WithReasoningEffort(context.Background(), tc.override)
			workerProgressSink(ctx, w, 1)(WorkerProgressEvent{Kind: "started"})
			if got.Model != model || got.ReasoningEffort != tc.want {
				t.Fatalf("metadata: %+v", got)
			}
			note := renderWorkerNotification(w, "done")
			if !strings.Contains(note, "<worker-model>"+model+"</worker-model>") || !strings.Contains(note, "<reasoning-effort>"+tc.want+"</reasoning-effort>") {
				t.Fatal(note)
			}
			handoff := prepareWorkerHandoff(w, "done")
			if handoff.snapshot.WorkerModel != model || handoff.snapshot.ReasoningEffort != tc.want || handoff.notification != note {
				t.Fatalf("prepared handoff lost UI metadata: %+v\n%s", handoff.snapshot, handoff.notification)
			}
			w.setState(func(w *Worker) {
				w.Runs = 2
				w.Status = "running"
				w.uiModel = "continuation-model"
				w.uiReasoningEffort = "continuation-effort"
			})
			if handoff.snapshot.WorkerModel != model || handoff.snapshot.ReasoningEffort != tc.want || handoff.withReport("done").notification != note {
				t.Fatal("continuation changed captured UI metadata")
			}
			if llm.ReasoningEffort() != "low" {
				t.Fatal("UI metadata changed global effort")
			}
		})
	}
}
