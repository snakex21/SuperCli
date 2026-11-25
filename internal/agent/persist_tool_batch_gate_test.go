package agent

import (
	"context"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// sessionRecorder is one saved conversation shared by two loops (the GUI
// builds a loop per turn; an earlier turn's loop delivers worker reports).
type sessionRecorder struct {
	recordingWriter
	id string
}

func (s *sessionRecorder) SessionID() string { return s.id }

// A worker report saved by another loop while this loop is between its tool
// calls and their results is written after the results, so a reload never
// finds the report inside the tool batch.
func TestReportFromAnotherLoopWaitsForTheToolBatch(t *testing.T) {
	store := &sessionRecorder{id: "gate-session"}
	newLoop := func() *Loop {
		l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "m"}, Registry: tools.NewRegistry(), Writer: store, MaxSteps: 2})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	current, earlier := newLoop(), newLoop()
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "c1", Name: "task", Arguments: "{}"}, {ID: "c2", Name: "send_message", Arguments: "{}"}}
	current.openToolBatchGate()
	current.persist(ctx, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls})
	earlier.persist(ctx, llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "error: worker worker-9 failed"})
	current.persist(ctx, llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "task", Content: "started"})
	current.persist(ctx, llm.Message{Role: llm.RoleTool, ToolCallID: "c2", Name: "send_message", Content: "continued"})
	if n := len(store.messages); n != 3 {
		t.Fatalf("the report was written inside the batch: %d rows", n)
	}
	current.closeToolBatchGate()
	got := store.messages
	if len(got) != 4 || got[1].ToolCallID != "c1" || got[2].ToolCallID != "c2" || got[3].Name != llm.WorkerNotificationName {
		t.Fatalf("saved order = %+v", got)
	}
	// With no batch open, reports are written at once.
	earlier.persist(ctx, llm.Message{Role: llm.RoleUser, Content: "later report"})
	if len(store.messages) != 5 {
		t.Fatal("a report without an open batch was held back")
	}
	// A run that ends without closing its batch releases the gate.
	current.openToolBatchGate()
	earlier.persist(ctx, llm.Message{Role: llm.RoleUser, Content: "held"})
	current.releaseConversation()
	if len(store.messages) != 6 {
		t.Fatal("ending the run did not write the held report")
	}
}
