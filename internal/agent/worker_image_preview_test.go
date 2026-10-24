package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// A worker's tool image is saved to the parent session's media folder and
// reported to the UI by path, without the worker writing transcript rows.
func TestWorkerToolImageReachesPreviewWithoutParentTranscript(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("project", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	reg := tools.NewRegistry()
	pixels := []byte("\x89PNG\r\n\x1a\nworker-fixture-pixels")
	reg.MustRegister(tools.Tool{Name: "fixture_image", Description: "fixture", Schema: "{}", ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "image read", Image: &tools.ImageContent{MediaType: "image/png", Data: pixels}}, nil
		}})
	reg.MarkAlwaysOn("fixture_image")
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "look", Name: "fixture_image", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{Content: "Seen.", FinishReason: "stop"}},
	}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 64)
	parent.SetExternalSink(events)
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Look at the image."}`))
	if err != nil || result.Err != nil {
		t.Fatalf("worker: %v %+v", err, result)
	}
	close(events)
	var images []string
	for ev := range events {
		if p, ok := ev.(WorkerProgressEvent); ok && p.Kind == "tool_result" {
			images = append(images, p.Images...)
		}
	}
	if len(images) != 1 {
		t.Fatalf("progress images = %v", images)
	}
	if got, err := os.ReadFile(images[0]); err != nil || string(got) != string(pixels) {
		t.Fatalf("image not stored in session media: %v", err)
	}
	child, ok := task.Workers.Get("worker-1")
	if !ok || child.Loop.writer != nil {
		t.Fatal("child acquired parent conversation writer")
	}
	history, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("worker polluted parent transcript: messages=%d err=%v", len(history), err)
	}
	child.stateMu.RLock()
	saved := child.uiActivity
	child.stateMu.RUnlock()
	var log workerUIActivity
	if err := json.Unmarshal(saved, &log); err != nil || len(log.Calls) != 1 ||
		len(log.Calls[0].Images) != 1 || log.Calls[0].Images[0] != filepath.Base(images[0]) {
		t.Fatalf("saved activity images = %+v err=%v", log.Calls, err)
	}
}
