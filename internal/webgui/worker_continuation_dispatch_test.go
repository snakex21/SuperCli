package webgui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sid := ""
	if err := eng.runStream(ctx, "implement spawn-fixture", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	continued := false
	if err := eng.runStream(ctx, "implement continue-fixture", sid, "", func(ev wireEvent) {
		if ev.Type == "question" {
			if err := eng.answerQuestion(ev.Question.ID, tools.AskAnswer{Selected: []string{"B"}}); err != nil {
				t.Error(err)
			}
		}
		if ev.Type == "tool_result" && ev.ID == "resume-call" {
			if ev.Err != "" {
				t.Errorf("continuation requires discovery: %s", ev.Err)
			}
			continued = ev.Err == "" && strings.Contains(ev.Output, "completed fixture")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !continued {
		t.Fatal("retained worker was not resumed")
	}
}
