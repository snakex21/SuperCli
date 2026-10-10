package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type bindingWriter struct{ saves int }

func (*bindingWriter) AppendMessage(context.Context, llm.Message) error       { return nil }
func (*bindingWriter) UpdateUsage(int, int) error                             { return nil }
func (w *bindingWriter) SaveToolOutput(context.Context, string, string) error { w.saves++; return nil }
func (*bindingWriter) ReadToolOutput(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected read")
}

type bindingTranscriptOnly struct{}

func (bindingTranscriptOnly) AppendMessage(context.Context, llm.Message) error { return nil }
func (bindingTranscriptOnly) UpdateUsage(int, int) error                       { return nil }

// An override need not be comparable. Binding must use configuration intent,
// not equality between arbitrary writer/store interface values.
type bindingOverride struct {
	target        *bindingWriter
	nonComparable []string
}

func (w bindingOverride) SaveToolOutput(ctx context.Context, h, s string) error {
	return w.target.SaveToolOutput(ctx, h, s)
}
func (w bindingOverride) ReadToolOutput(ctx context.Context, h string) (string, error) {
	return w.target.ReadToolOutput(ctx, h)
}

func bindingLoop(t *testing.T, writer SessionWriter, override tools.OutputPersistence) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "fixture_evidence", Description: "fixture", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: strings.Repeat("recorded output\n", 1000)}, nil
	}})
	reg.MarkAlwaysOn("fixture_evidence")
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("binding"), Registry: reg, Writer: writer, ToolOutputs: override})
	if err != nil {
		t.Fatal(err)
	}
	return loop
}
func bindingRun(t *testing.T, l *Loop) string {
	t.Helper()
	result := l.invoke(context.Background(), llm.ToolCall{ID: "produce", Name: "fixture_evidence", Arguments: "{}"}, make(chan Event, 8))
	if result.failed || len(result.followUps) != 1 {
		t.Fatalf("tool failed: %+v", result)
	}
	return result.followUps[0].Content
}

func TestResumeOutputBindingConfiguration(t *testing.T) {
	ctx := context.Background()
	history := []llm.Message{{Role: llm.RoleUser, Content: "Continue."}}
	for _, initialKind := range []string{"none", "transcript", "durable"} {
		t.Run("automatic/"+initialKind, func(t *testing.T) {
			previous, current := &bindingWriter{}, &bindingWriter{}
			var initial SessionWriter
			switch initialKind {
			case "transcript":
				initial = bindingTranscriptOnly{}
			case "durable":
				initial = previous
			}
			loop := bindingLoop(t, initial, nil)
			if err := loop.ResumeConversation(ctx, current, history, nil); err != nil {
				t.Fatal(err)
			}
			if got := bindingRun(t, loop); !strings.Contains(got, "saved for later turns") {
				t.Fatal("new writer did not supply persistence")
			}
			if previous.saves != 0 || current.saves != 1 {
				t.Fatalf("old=%d current=%d", previous.saves, current.saves)
			}
			if err := loop.ResumeConversation(ctx, bindingTranscriptOnly{}, history, nil); err != nil {
				t.Fatal(err)
			}
			if got := bindingRun(t, loop); !strings.Contains(got, "in memory only") {
				t.Fatal("old durable owner survived transcript-only switch")
			}
			if previous.saves != 0 || current.saves != 1 {
				t.Fatal("output written to an inactive writer")
			}
			if err := loop.ResumeConversation(ctx, previous, history, nil); err != nil {
				t.Fatal(err)
			}
			bindingRun(t, loop)
			if previous.saves != 1 || current.saves != 1 {
				t.Fatal("durable output persistence did not resume")
			}
		})
	}
	for _, kind := range []string{"same as initial writer", "distinct non-comparable"} {
		t.Run("explicit/"+kind, func(t *testing.T) {
			previous, current, external := &bindingWriter{}, &bindingWriter{}, &bindingWriter{}
			var override tools.OutputPersistence = previous
			target := previous
			if kind == "distinct non-comparable" {
				override = bindingOverride{target: external, nonComparable: []string{"scope"}}
				target = external
			}
			loop := bindingLoop(t, previous, override)
			if err := loop.ResumeConversation(ctx, current, history, nil); err != nil {
				t.Fatal(err)
			}
			bindingRun(t, loop)
			if target.saves != 1 || current.saves != 0 {
				t.Fatal("explicit owner was replaced")
			}
		})
	}
}

func TestRejectedResumeKeepsOutputBinding(t *testing.T) {
	for _, kind := range []string{"busy", "canceled", "nil writer", "empty history", "pending history", "dirty projection"} {
		t.Run(kind, func(t *testing.T) {
			previous, next := &bindingWriter{}, &bindingWriter{}
			loop := bindingLoop(t, previous, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var target SessionWriter = next
			history := []llm.Message{{Role: llm.RoleUser, Content: "Next conversation."}}
			switch kind {
			case "busy":
				loop.sessionBusy.Store(true)
			case "canceled":
				cancel()
			case "nil writer":
				target = nil
			case "empty history":
				history = nil
			case "pending history":
				loop.persistHealth.pending = []pendingAppend{{Message: llm.Message{Role: llm.RoleUser, Content: "unsaved"}, Writer: previous}}
			case "dirty projection":
				loop.persistHealth.projectionDirty = true
			}
			if err := loop.ResumeConversation(ctx, target, history, nil); err == nil {
				t.Fatal("resume unexpectedly accepted")
			}
			bindingRun(t, loop)
			if previous.saves != 1 || next.saves != 0 {
				t.Fatal("rejected switch rebound output persistence")
			}
		})
	}
}
