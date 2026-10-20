package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Real task execution, model delivery, canonical SQLite history and resumed
// projection must agree; a smaller provider view must not become a shorter UI.
func TestMediumWorkerReportProductionDeliveryAndResume(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, async := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/async=%t", thin, async), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				store, err := session.OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				sess, err := store.Create("fixture", "fixture", "medium worker compression")
				if err != nil {
					t.Fatal(err)
				}
				writer := session.NewWriter(store, sess.ID)
				report := strings.TrimSpace(strings.Repeat("Kontrola: żółć 日本語 😀 — plik bez zmian.\n", 70) +
					"P2: EXACT_MIDDLE_FINDING remains unresolved; do not claim success.\n" +
					strings.Repeat("Verification: inspected fixture, no extra mutation.\n", 20))
				registry := tools.NewRegistry()
				parentProvider := &stubProvider{name: "medium-parent", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "review", Name: "task", Arguments: fmt.Sprintf(`{"agent":"review","prompt":"Review fixture","async":%t}`, async)}, FinishReason: "tool_calls"}},
					{{Content: "Review complete; the P2 remains unresolved.", FinishReason: "stop"}},
				}}
				child := &stubProvider{name: "medium-child", scripts: [][]llm.Delta{{{Content: report, FinishReason: "stop"}}}}
				parent, err := NewLoop(LoopConfig{Provider: parentProvider, Registry: registry, Writer: writer,
					System: "Stable policy; preserve unresolved findings.", ThinTools: thin, StableToolset: thin, CatalogHoist: thin})
				if err != nil {
					t.Fatal(err)
				}
				external := make(chan Event, 32)
				parent.SetExternalSink(external)
				specs := NewSubAgentRegistry()
				MustRegisterAll(specs, BuiltinSubAgents())
				task, err := NewAgentTool(specs, parent, registry, child, nil, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				registry.MustRegister(task.Spec())
				registry.MarkAlwaysOn("task")
				release := make(chan struct{})
				var once sync.Once
				releaseChild := func() { once.Do(func() { close(release) }) }
				t.Cleanup(releaseChild)
				uiText := ""
				if async {
					child.onCalled = func(int) { <-release }
					parentProvider.onCalled = func(index int) {
						if index != 1 {
							return
						}
						releaseChild()
						for {
							select {
							case event := <-external:
								if notice, ok := event.(WorkerNotificationEvent); ok {
									uiText = notice.Text
									return
								}
							case <-ctx.Done():
								t.Error("worker notification timed out")
								return
							}
						}
					}
				}
				events, err := parent.Run(ctx, "Delegate a review and report all findings.")
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range drainEvents(t, events) {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
				for len(external) > 0 {
					if notice, ok := (<-external).(WorkerNotificationEvent); ok {
						if uiText != "" {
							t.Fatal("duplicate UI report")
						}
						uiText = notice.Text
					}
				}
				wantRequests := 2
				if async {
					wantRequests = 3 // late report, as in the unchanged baseline
				}
				if len(parentProvider.reqs) != wantRequests || len(child.reqs) != 1 || !strings.Contains(uiText, report) {
					t.Fatalf("flow/UI changed: parent=%d child=%d full_UI=%t", len(parentProvider.reqs), len(child.reqs), strings.Contains(uiText, report))
				}
				modelText := ""
				for _, message := range parentProvider.reqs[len(parentProvider.reqs)-1] {
					if strings.Contains(message.Content, workerReportEncodingPrefix) {
						if modelText != "" {
							t.Fatal("compact report duplicated in request")
						}
						modelText = message.Content
					}
				}
				if modelText == "" || len(modelText) >= len(uiText) || decodeMediumReportExperiment(t, modelText) != uiText {
					t.Fatal("real task did not deliver the complete, reversible compact report")
				}
				canonical, err := store.ReadMessages(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				fullCount := 0
				for _, message := range canonical {
					if strings.Contains(message.Content, workerReportEncodingPrefix) {
						t.Fatal("encoded model representation leaked into canonical UI history")
					}
					if message.Content == uiText {
						fullCount++
					}
				}
				if fullCount != 1 {
					t.Fatalf("canonical full reports=%d, want one", fullCount)
				}
				projected, err := store.ReadModelContext(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				compactCount := 0
				for _, message := range projected {
					if message.Content == modelText {
						compactCount++
					}
				}
				if compactCount != 1 {
					t.Fatalf("resumable compact reports=%d, want one", compactCount)
				}
				resumedProvider := &stubProvider{name: "medium-resumed", scripts: [][]llm.Delta{{{Content: "The P2 still needs resolution.", FinishReason: "stop"}}}}
				resumed, err := NewLoop(LoopConfig{Provider: resumedProvider, Registry: tools.NewRegistry(), Writer: writer,
					InitialMessages: projected, ThinTools: thin, StableToolset: thin, CatalogHoist: thin})
				if err != nil {
					t.Fatal(err)
				}
				drainEvents(t, mustRun(t, resumed, "Continue inspection only."))
				stable := false
				if len(resumedProvider.reqs) == 1 {
					for i := 0; i+len(projected) <= len(resumedProvider.reqs[0]); i++ {
						if reflect.DeepEqual(resumedProvider.reqs[0][i:i+len(projected)], projected) {
							stable = true
							break
						}
					}
				}
				if !stable {
					t.Fatal("resume rewrote the historical model prefix")
				}
				t.Logf("full_report=%d model_report=%d requests=%d child_requests=1 canonical_full=1 projection_compact=1 prefix_stable=true", len(uiText), len(modelText), len(parentProvider.reqs))
			})
		}
	}
}

func TestMediumWorkerReportContinuedWorkerKeepsFullHistory(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess, err := store.Create("fixture", "fixture", "continued report")
	if err != nil {
		t.Fatal(err)
	}
	report := strings.TrimSpace(strings.Repeat("Inspection: continued fixture unchanged.\n", 130) + "P2: continued EXACT_MIDDLE_FINDING remains unresolved.")
	registry := tools.NewRegistry()
	provider := &stubProvider{name: "continuation-parent", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "first", Name: "task", Arguments: `{"agent":"review","prompt":"Review fixture","async":false}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "second", Name: "send_message", Arguments: `{"to":"worker-1","message":"Recheck fixture","mode":"continue"}`}, FinishReason: "tool_calls"}},
		{{Content: "The continued review still has a P2.", FinishReason: "stop"}},
	}}
	child := &stubProvider{name: "continuation-child", scripts: [][]llm.Delta{
		{{Content: "Initial fixture findings retained.", FinishReason: "stop"}},
		{{Content: report, FinishReason: "stop"}},
	}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, Writer: session.NewWriter(store, sess.ID), System: "Keep unresolved findings."})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, registry, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	registry.MustRegister(task.Spec())
	registry.MustRegister(NewSendMessageTool(task.Workers).Spec())
	registry.MarkAlwaysOn("task")
	registry.MarkAlwaysOn("send_message")
	for _, event := range drainEvents(t, mustRun(t, parent, "Review the fixture, recheck the findings, then report.")) {
		if failure, ok := event.(ErrorEvent); ok {
			t.Fatal(failure.Err)
		}
	}
	if len(provider.reqs) != 3 || len(child.reqs) != 2 {
		t.Fatalf("continuation calls changed: parent=%d child=%d", len(provider.reqs), len(child.reqs))
	}
	compact := ""
	for _, message := range provider.reqs[2] {
		if message.Role == llm.RoleTool && message.Name == "send_message" {
			compact = message.Content
		}
	}
	if !strings.Contains(compact, workerReportEncodingPrefix) {
		t.Fatal("continued worker result was not encoded")
	}
	full := decodeMediumReportExperiment(t, compact)
	if !strings.Contains(full, report) {
		t.Fatal("continued result was not fully reversible")
	}
	rows, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Role == string(llm.RoleTool) && row.Name == "send_message" {
			found = row.Content == full
		}
	}
	if !found {
		t.Fatal("continued worker canonical history lost the full result")
	}
}
