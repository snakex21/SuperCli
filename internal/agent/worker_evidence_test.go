package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestWorkerEvidenceAvailableWithoutWorkerInference(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "persisted"}[persistent], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "config.go")
			if err := os.WriteFile(path, []byte("package config\nconst AuditValue = 731\n"+strings.Repeat("// archived evidence line\n", 60)), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			read := tools.NewReadLines(root).Spec()
			fn := read.Fn
			reads := 0
			read.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) { reads++; return fn(ctx, raw) }
			reg.MustRegister(read)
			reg.MarkAlwaysOn(read.Name)
			child := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{Content: "PRIVATE_COMMENTARY"}, {ToolCall: &llm.ToolCall{ID: "read-1", Name: "read_lines", Arguments: `{"file":"config.go","from":1,"to":100}`}}, {FinishReason: "tool_calls"}},
				{{Content: "Configuration inspected in config.go."}, {FinishReason: "stop"}},
				{{Content: "No additional inspection needed."}, {FinishReason: "stop"}},
			}}
			var writer *session.Writer
			if persistent {
				store, err := session.OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				sess, err := store.Create("fixture", "fixture", "")
				if err != nil {
					t.Fatal(err)
				}
				writer = session.NewWriter(store, sess.ID)
			}
			cfg := LoopConfig{Provider: child, Registry: reg, BaseDir: root}
			if writer != nil {
				cfg.Writer = writer
			}
			parent, err := NewLoop(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sub := NewSubAgentRegistry()
			MustRegisterAll(sub, BuiltinSubAgents())
			task, err := NewAgentTool(sub, parent, reg, child, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			res, err := task.execute(ctx, json.RawMessage(`{"agent":"explore","prompt":"PRIVATE_PROMPT inspect config.go"}`))
			if err != nil || res.Err != nil {
				t.Fatalf("%+v %v", res, err)
			}
			if !strings.Contains(res.RetainedText, "AuditValue = 731") || strings.Contains(res.Text, "AuditValue") {
				t.Fatalf("snapshot missing or inline: %+v", res)
			}
			for _, private := range []string{"PRIVATE_PROMPT", "PRIVATE_COMMENTARY"} {
				if strings.Contains(res.RetainedText, private) {
					t.Fatalf("private worker conversation included: %s", private)
				}
			}
			saveCtx := ctx
			if parent.toolOutputs != nil {
				saveCtx = tools.WithOutputPersistence(ctx, parent.toolOutputs)
			}
			visible := reg.ModelResultContentContext(saveCtx, "task", res)
			handle := handleInOutput(visible)
			if handle == "" {
				t.Fatal("parent did not retain attachment")
			}
			if strings.Contains(visible, "AuditValue") {
				t.Fatal("raw observations leaked into parent prompt")
			}
			if err := os.WriteFile(path, []byte("package config\nconst AuditValue = 999\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if persistent {
				fresh, err := NewLoop(LoopConfig{Provider: child, Registry: tools.NewRegistry(), Writer: writer})
				if err != nil {
					t.Fatal(err)
				}
				parent = fresh
			}
			raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "AuditValue"})
			got := parent.invoke(ctx, llm.ToolCall{ID: "inspect-snapshot", Name: "read_output", Arguments: string(raw)}, make(chan Event, 4))
			if got.failed || len(got.followUps) != 1 || !strings.Contains(got.followUps[0].Content, "AuditValue = 731") || strings.Contains(got.followUps[0].Content, "AuditValue = 999") {
				t.Fatalf("wrong historical observation: %+v", got)
			}
			if reads != 1 || child.calls != 2 {
				t.Fatalf("evidence triggered fresh work: reads=%d inference=%d", reads, child.calls)
			}
			follow, err := NewSendMessageTool(task.Workers).execute(ctx, json.RawMessage(`{"to":"worker-1","message":"continue without tools"}`))
			if err != nil || follow.Err != nil {
				t.Fatalf("%+v %v", follow, err)
			}
			if follow.RetainedText != "" || strings.Contains(follow.Text, "Attached:") {
				t.Fatal("no-tool continuation repeated old observations")
			}
		})
	}
}

func TestWorkerEvidenceKeepsFailuresAndBounds(t *testing.T) {
	var evidence workerEvidenceLog
	evidence.add(ToolCallEvent{Name: "tool_search", Args: "{}"}, ToolResultEvent{Output: "SCHEMA_NOISE"})
	evidence.add(ToolCallEvent{Name: "ask_user", Args: "SECRET_QUESTION"}, ToolResultEvent{Output: "SECRET_ANSWER"})
	if evidence.text() != "" {
		t.Fatal("meta tool noise retained")
	}
	for i := 0; i < 40; i++ {
		evidence.add(ToolCallEvent{Name: "read_lines", Args: `{"path":"test.go"}`}, ToolResultEvent{Output: strings.Repeat("ą", 6000)})
	}
	evidence.add(ToolCallEvent{Name: "ctx_execute", Args: `{"command":["go","test","./..."]}`}, ToolResultEvent{Output: "FAILED_ASSERTION", Err: errors.New("exit code 1")})
	text := evidence.text()
	if !strings.Contains(text, "older tool observations omitted") || !strings.Contains(text, "FAILED_ASSERTION") || !strings.Contains(text, "ERROR: exit code 1") {
		t.Fatal("failure or bounds not explicit")
	}
	if len(text) > workerEvidenceBytes+256 || !utf8.ValidString(text) {
		t.Fatalf("unsafe bounded snapshot: bytes=%d utf8=%v", len(text), utf8.ValidString(text))
	}
	w := &Worker{ID: "worker-1", Agent: "code", Status: "failed", Runs: 1, lastEvidence: text}
	result := workerResult(w, "Check failed.", errors.New("worker failed"))
	if result.Err == nil || !strings.Contains(result.RetainedText, "historical snapshots") || !strings.Contains(result.RetainedText, "exit code 1") {
		t.Fatal("failed run lost evidence")
	}
	w.Status = "running"
	if workerResult(w, "", errors.New("busy")).RetainedText != "" {
		t.Fatal("busy call attached another invocation's evidence")
	}
}

func TestWorkerEvidenceTinyObservationStaysInline(t *testing.T) {
	w := &Worker{ID: "worker-1", Agent: "explore", Status: "done", Runs: 1, lastEvidence: "== read_lines config.go ==\n2 | const AuditValue = 731"}
	got := workerResult(w, "Read config.go.", nil)
	if got.RetainedText != "" || !strings.Contains(got.Text, "AuditValue = 731") || !strings.Contains(got.Text, "historical snapshots") {
		t.Fatalf("tiny evidence needs another retrieval: %+v", got)
	}
	if len(got.Text)-len(renderWorkerNotification(w, "Read config.go.")) > workerEvidenceInlineBytes {
		t.Fatal("inline cap exceeded")
	}
}

func TestWorkerEvidenceBackgroundAttachment(t *testing.T) {
	root := t.TempDir()
	body := "package config\nconst AuditValue = 731\n" + strings.Repeat("// evidence line\n", 100)
	if err := os.WriteFile(filepath.Join(root, "config.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadLines(root).Spec())
	reg.MarkAlwaysOn("read_lines")
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "read_lines", Arguments: `{"file":"config.go","from":1,"to":200}`}}, {FinishReason: "tool_calls"}},
		{{Content: "Inspected config.go."}, {FinishReason: "stop"}},
	}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	parent.SetExternalSink(events)
	sub := NewSubAgentRegistry()
	MustRegisterAll(sub, BuiltinSubAgents())
	task, err := NewAgentTool(sub, parent, reg, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	task.MaxSteps = 4
	res, err := task.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"inspect config.go","async":true}`))
	if err != nil || res.Err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case ev := <-events:
			if _, ok := ev.(WorkerNotificationEvent); !ok {
				continue
			}
			if len(parent.Messages) != 1 {
				t.Fatalf("messages=%d", len(parent.Messages))
			}
			notice := parent.Messages[0].Content
			handle := handleInOutput(notice)
			if handle == "" || strings.Contains(notice, "AuditValue") {
				t.Fatalf("bad attachment: %s", notice)
			}
			raw, _ := json.Marshal(map[string]string{"handle": handle, "query": "AuditValue"})
			got, err := reg.Execute(context.Background(), "read_output", raw)
			if err != nil || got.Err != nil || !strings.Contains(got.Text, "731") {
				t.Fatalf("%+v %v", got, err)
			}
			return
		case <-timeout.C:
			t.Fatal("worker did not deliver completion")
		}
	}
}

func BenchmarkWorkerEvidenceHandoff(b *testing.B) {
	for _, name := range []string{"report-only", "inline", "stored-memory", "stored-session"} {
		b.Run(name, func(b *testing.B) {
			reg := tools.NewRegistry()
			reg.EnsureReadOutput()
			ctx := context.Background()
			w := &Worker{ID: "worker-1", Agent: "explore", Status: "done", Runs: 1}
			switch name {
			case "inline":
				w.lastEvidence = "== read_lines config.go ==\n2 | const AuditValue = 731"
			case "stored-memory", "stored-session":
				w.lastEvidence = strings.Repeat("observed source line\n", 2500)
			}
			if name == "stored-session" {
				store, err := session.OpenStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				defer store.Close()
				sess, err := store.Create("fixture", "fixture", "")
				if err != nil {
					b.Fatal(err)
				}
				ctx = tools.WithOutputPersistence(ctx, session.NewWriter(store, sess.ID))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := workerResult(w, "Inspected config.go.", nil)
				_ = reg.ModelResultContentContext(ctx, "task", result)
			}
		})
	}
}
