package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func projectionReuseRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "search_history", Description: "history", Schema: "{}", ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	return reg
}

func TestCompletedProjectionReusesExactViewAndPreservesArchive(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "echo", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	history := []llm.Message{{Role: llm.RoleUser, Content: "Keep every requirement; leave the docs step pending."}}
	history = append(history, completedRead("old", "old.go", strings.Repeat("observed file value; ", 1000))...)
	history = append(history, llm.Message{Role: llm.RoleUser, Content: "Latest correction: never publish."})
	for _, msg := range history {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	archivedBefore, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	prov := &stubProvider{scripts: [][]llm.Delta{{{Content: "The docs remain pending and publishing is forbidden.", FinishReason: "stop"}}}}
	l, err := NewLoop(LoopConfig{Provider: prov, Registry: projectionReuseRegistry(), Writer: writer, InitialMessages: append([]llm.Message(nil), history...)})
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, mustRun(t, l, "Report the state without doing another operation."))
	projected, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := l.resolvedToolProviderView(l.VisibleMessages()); !reflect.DeepEqual(projected, want) {
		t.Fatalf("saved provider view changed: got %d, want %d messages", len(projected), len(want))
	}
	archive, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || len(archive) != len(history)+2 {
		t.Fatalf("archive length changed: %d, %v", len(archive), err)
	}
	if !reflect.DeepEqual(archive[:len(archivedBefore)], archivedBefore) || prov.calls != 1 {
		t.Fatal("canonical tool evidence/instructions changed or an extra inference ran")
	}
}

func TestCompletedProjectionFailureRebuildsAfterNewEvidence(t *testing.T) {
	w := &flakyWriter{failProjection: true}
	l, _ := newPersistTestLoop(t, w)
	l.registry = projectionReuseRegistry()
	l.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: "Preserve all instructions."}, {Role: llm.RoleUser, Content: "Earlier task."}}, completedRead("old", "old.go", strings.Repeat("old result ", 800))...)
	l.persistCompletedProjection(context.Background())
	if !l.PersistStatus().ProjectionDirty {
		t.Fatal("failed saved view did not schedule recovery")
	}
	correction := llm.Message{Role: llm.RoleUser, Content: "NEW: keep the failed export pending; use C:\\Próba 😀\\data."}
	activeCall := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "ctx_execute", Arguments: `{"command":"export"}`}}}
	activeResult := llm.Message{Role: llm.RoleTool, Name: "ctx_execute", ToolCallID: "active", Content: "error: export failed; task incomplete"}
	l.Messages = append(l.Messages, correction, activeCall, activeResult)
	before, _ := json.Marshal(l.Messages)
	w.failProjection = false
	l.retryDirtyProjection(context.Background())
	if len(w.projections) != 1 {
		t.Fatalf("recovery saved %d views", len(w.projections))
	}
	got := w.projections[0]
	if len(got) < 3 || !reflect.DeepEqual(got[len(got)-3:], []llm.Message{correction, activeCall, activeResult}) {
		t.Fatal("recovery reused stale memory instead of the newest instructions and active tool tail")
	}
	for _, msg := range got {
		if msg.Role == llm.RoleSystem {
			t.Fatal("saved a stale leading system instruction")
		}
	}
	after, _ := json.Marshal(l.Messages)
	if string(before) != string(after) || l.PersistStatus().ProjectionDirty {
		t.Fatal("canonical history changed or recovery remained dirty")
	}
}

func TestCompletedProjectionWithoutProjectionWriterDoesNoWork(t *testing.T) {
	l := &Loop{registry: projectionReuseRegistry(), writer: &recordingWriter{}, Messages: projectionScanFixture(400, true, true, true)}
	if allocations := testing.AllocsPerRun(100, func() { l.persistCompletedProjection(context.Background()) }); allocations != 0 {
		t.Fatalf("writer cannot store a view, but preparation allocated %.0f times", allocations)
	}
}

type projectionBoundaryBenchWriter struct{ messages int }

func (*projectionBoundaryBenchWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (*projectionBoundaryBenchWriter) UpdateUsage(int, int) error                       { return nil }
func (w *projectionBoundaryBenchWriter) SaveContextProjection(_ context.Context, msgs []llm.Message) error {
	w.messages = len(msgs)
	return nil
}

func BenchmarkCompletedProjectionBoundary(b *testing.B) {
	for _, mode := range []string{"previous", "reused"} {
		b.Run(mode, func(b *testing.B) {
			l := &Loop{registry: projectionReuseRegistry(), writer: &projectionBoundaryBenchWriter{}, Messages: projectionScanFixture(400, true, true, true)}
			b.ReportAllocs()
			for b.Loop() {
				if mode == "previous" {
					visible := l.VisibleMessages()
					if len(l.resolvedToolProviderView(visible)) < len(visible) {
						l.persistProjection(context.Background())
					}
				} else {
					l.persistCompletedProjection(context.Background())
				}
			}
		})
	}
}
