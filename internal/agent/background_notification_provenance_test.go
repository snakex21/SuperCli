package agent

import (
	"context"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestBackgroundWorkerReportHasHostProvenance(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{}, Registry: tools.NewRegistry(), System: "base"})
	if err != nil {
		t.Fatal(err)
	}
	deliver := loop.backgroundDelivery(context.Background())
	deliver(context.Background(), "host worker report")
	if len(loop.Messages) != 2 || loop.Messages[1].Role != llm.RoleUser || loop.Messages[1].Name != llm.WorkerNotificationName {
		t.Fatalf("worker report lacks stable provenance: %+v", loop.Messages)
	}
	loop.InjectUserMessage(context.Background(), "ordinary out-of-band instruction")
	if len(loop.Messages) != 3 || loop.Messages[2].Name != "" {
		t.Fatal("generic user injection was incorrectly marked as a worker")
	}
}
