package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func workerSourceFixture(t *testing.T) (*AgentTool, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"cache/store.go": "package cache\nconst expiry = 100\n", "cache/store_test.go": "package cache\n// expiry boundary test\n", "unrelated.go": "package unrelated\n// NOT_FOR_WORKER\n"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg := tools.NewRegistry()
	reader := tools.NewReadMany(root).Spec()
	reg.MustRegister(reader)
	call := llm.ToolCall{ID: "known", Name: "read_many", Arguments: `{"reads":"cache/store.go | cache/store_test.go | unrelated.go"}`}
	result, err := reader.Fn(context.Background(), json.RawMessage(call.Arguments))
	if err != nil || result.Err != nil {
		t.Fatalf("reader: %v %v", err, result.Err)
	}
	parent := &Loop{baseDir: root, registry: reg, Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "fix cache; keep tests unchanged"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
		{Role: llm.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: result.Text},
	}}
	return &AgentTool{ParentLoop: parent, BaseRegistry: reg}, root
}

func TestWorkerSourceContextTransfersOnlyCurrentRelevantEvidence(t *testing.T) {
	a, root := workerSourceFixture(t)
	before, _ := json.Marshal(a.ParentLoop.Messages)
	task := "Fix cache/store.go and preserve cache/store_test.go"
	got := a.workerSourceContext(context.Background(), task, SubAgent{})
	if !strings.Contains(got, "const expiry = 100") || !strings.Contains(got, "expiry boundary test") || strings.Contains(got, "NOT_FOR_WORKER") {
		t.Fatalf("incorrect evidence: %s", got)
	}
	if len(got) > workerSourceBytes || got != a.workerSourceContext(context.Background(), task, SubAgent{}) {
		t.Fatal("handoff is unbounded or unstable")
	}
	after, _ := json.Marshal(a.ParentLoop.Messages)
	if string(before) != string(after) {
		t.Fatal("handoff changed parent history")
	}
	if err := os.WriteFile(filepath.Join(root, "cache/store.go"), []byte("package cache\nconst expiry = 200\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got = a.workerSourceContext(context.Background(), task, SubAgent{})
	if strings.Contains(got, "const expiry") || !strings.Contains(got, "expiry boundary test") {
		t.Fatalf("stale source shared or valid sibling lost: %s", got)
	}
}

func TestWorkerSourceContextHonorsHiddenEvidenceRestrictionsAndCancellation(t *testing.T) {
	a, _ := workerSourceFixture(t)
	if got := a.workerSourceContext(context.Background(), "cache/store.go.old", SubAgent{}); got != "" {
		t.Fatal("substring was treated as a file mention")
	}
	if got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{AllowedTools: []string{"web_fetch"}}); got != "" {
		t.Fatal("file evidence leaked to a worker without source tools")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := a.workerSourceContext(ctx, "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("canceled handoff continued")
	}
	if err := a.ParentLoop.HideRange(1, 3); err != nil {
		t.Fatal(err)
	}
	if got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("hidden history was resurrected")
	}
}

func TestWorkerSourceContextRejectsSymlinkOutsideWorkspace(t *testing.T) {
	a, root := workerSourceFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package cache\nconst expiry = 100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cache/store.go")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("out-of-workspace source shared through symlink")
	}
}

func TestWorkerSourceContextPrefersNewerEvidenceAfterEdit(t *testing.T) {
	a, root := workerSourceFixture(t)
	if err := os.WriteFile(filepath.Join(root, "cache/store.go"), []byte("package cache\nconst expiry = 200\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reader := tools.NewReadMany(root).Spec()
	call := llm.ToolCall{ID: "newer", Name: reader.Name, Arguments: `{"reads":"cache/store.go"}`}
	result, err := reader.Fn(context.Background(), json.RawMessage(call.Arguments))
	if err != nil || result.Err != nil {
		t.Fatalf("reader: %v %v", err, result.Err)
	}
	a.ParentLoop.Messages = append(a.ParentLoop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result.Text})
	got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{})
	if !strings.Contains(got, "expiry = 200") || strings.Contains(got, "expiry = 100") {
		t.Fatalf("new evidence did not supersede stale observation: %s", got)
	}
}

func TestWorkerSourceContextBoundsLargeEvidenceAndSkipsDirectories(t *testing.T) {
	a, root := workerSourceFixture(t)
	var code strings.Builder
	for i := 0; i < 500; i++ {
		code.WriteString("// long source line with meaningful facts and unchanged source\n")
	}
	path := filepath.Join(root, "cache/store.go")
	if err := os.WriteFile(path, []byte(code.String()), 0600); err != nil {
		t.Fatal(err)
	}
	reader := tools.NewReadLines(root).Spec()
	call := llm.ToolCall{ID: "large", Name: reader.Name, Arguments: `{"file":"cache/store.go","from":1,"to":500}`}
	result, err := reader.Fn(context.Background(), json.RawMessage(call.Arguments))
	if err != nil || result.Err != nil {
		t.Fatalf("reader: %v %v", err, result.Err)
	}
	a.ParentLoop.Messages = append(a.ParentLoop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result.Text})
	got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{})
	if len(got) == 0 || len(got) > workerSourceBytes || !strings.Contains(got, "omitted") {
		t.Fatalf("large evidence not explicitly bounded (%d): %s", len(got), got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if got := a.workerSourceContext(context.Background(), "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("non-regular source transferred")
	}
}

func TestTaskAutomaticallyPassesSourceWithoutSharingConversation(t *testing.T) {
	a, _ := workerSourceFixture(t)
	specs := NewSubAgentRegistry()
	specs.MustRegister(SubAgent{Name: "code", Description: "implement"})
	var child *Loop
	at, err := NewAgentTool(specs, a.ParentLoop, a.BaseRegistry, &stubReplyProvider{name: "fixture"}, nil, func(cfg LoopConfig) (*Loop, error) {
		if len(cfg.InitialMessages) != 0 {
			t.Fatal("entire conversation was shared")
		}
		var err error
		child, err = childLoopFactory("done", nil)(cfg)
		return child, err
	})
	if err != nil {
		t.Fatal(err)
	}
	at.Preflight = func() string { return "Unrelated modified path: unrelated.go" }
	result, err := at.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"Fix cache/store.go, preserve cache/store_test.go"}`))
	if err != nil || result.Err != nil || child == nil {
		t.Fatalf("task: %v %v", err, result.Err)
	}
	found := false
	for _, message := range child.Messages {
		found = found || strings.Contains(message.Content, "[Source evidence from coordinator]")
		if strings.Contains(message.Content, "NOT_FOR_WORKER") {
			t.Fatal("preflight paths pulled unrelated source into the task")
		}
	}
	if !found {
		t.Fatal("worker received no existing source evidence")
	}
}
