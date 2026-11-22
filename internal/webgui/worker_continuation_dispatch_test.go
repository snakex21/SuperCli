package webgui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// This is the envelope a stable tool catalog uses, rather than a direct native
// send_message call. Every run creates a fresh parent registry.
type workerContinuationEnvelopeProvider struct{ resumedQuestionProvider }

func (p *workerContinuationEnvelopeProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	deltas, err := p.resumedQuestionProvider.Complete(ctx, msgs, defs)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.Delta, 4)
	for delta := range deltas {
		if call := delta.ToolCall; call != nil && call.Name == "send_message" {
			args, _ := json.Marshal(map[string]any{"tool": call.Name, "args": json.RawMessage(call.Arguments)})
			delta.ToolCall = &llm.ToolCall{ID: call.ID, Name: "invoke_tool", Arguments: string(args)}
		}
		out <- delta
	}
	close(out)
	return out, nil
}

func TestWorkerContinuationEnvelopeSurvivesNewWebRun(t *testing.T) {
	root := t.TempDir()
	eng, err := NewEngine(echoConfig(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.prov = &workerContinuationEnvelopeProvider{}
	// The stable-catalog envelope (invoke_tool -> send_message) still reaches
	// the retained worker from a fresh run's registry, without discovery; the
	// continuation then runs in the background and reports back.
	continued, events := resumeWorkerInNewWebRun(t, eng)
	if continued.Err != "" {
		t.Fatalf("continuation requires discovery: %s", continued.Err)
	}
	if !strings.Contains(continued.Output, "continued in background") {
		t.Fatalf("retained worker was not resumed: %+v", continued)
	}
	reported := false
	for _, ev := range events {
		reported = reported || ev.Type == "worker" && ev.ID == "worker-1" && ev.Run == 2 && strings.Contains(ev.Text+ev.Output, "completed fixture")
	}
	if !reported {
		t.Fatal("resumed worker's report did not reach the conversation")
	}
}
