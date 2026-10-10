package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestZipExtractionExpiresCommandFailuresAndWorkerEvidence(t *testing.T) {
	for _, dispatch := range []string{"native", "invoke"} {
		t.Run(dispatch, func(t *testing.T) {
			root := zipScheduleArchive(t)
			if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("probe"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadZip(root, 0).Spec())
			reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "check extracted fixture", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}},"required":["command"]}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				if _, err := os.Stat(filepath.Join(root, "dest/file.txt")); err != nil {
					return tools.Result{Err: errors.New("missing extracted source")}, nil
				}
				return tools.Result{Text: `{"exit_code":0}`}, nil
			}})
			reg.Activate("read_zip")
			reg.Activate("ctx_execute")
			reg.MustRegister(NewInvokeTool(reg).Spec())
			l, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			command := `{"command":["go","test","./..."]}`
			parent.identicalFails.recordFailure("ctx_execute", command)
			parent.identicalFails.recordFailure("ctx_execute", command)
			ctx := withWorkerMutationObserver(context.Background(), parent.workerMutationObserver(context.Background()))
			invoke := func(name, args string) toolResult {
				call := llm.ToolCall{ID: name, Name: name, Arguments: args}
				if dispatch == "invoke" {
					raw, _ := json.Marshal(map[string]any{"tool": name, "args": json.RawMessage(args)})
					call.Name = "invoke_tool"
					call.Arguments = string(raw)
					call = l.resolveInvokeToolCalls([]llm.ToolCall{call})[0]
				}
				return l.invoke(ctx, call, make(chan Event, 32))
			}
			for i := 0; i < 2; i++ {
				if res := invoke("ctx_execute", command); !res.failed {
					t.Fatal("check should fail before extraction")
				}
			}
			if !l.identicalFails.shouldBlock("ctx_execute", command) {
				t.Fatal("missing failure guard")
			}
			if res := invoke("read_zip", `{"path":"a.zip","action":"list"}`); res.failed || !res.observation.valid {
				t.Fatalf("list must remain observation: %+v", res)
			}
			if res := invoke("read_zip", `{"path":"a.zip","action":"read","pattern":"file.txt"}`); res.failed || !res.observation.valid {
				t.Fatalf("text read must remain observation: %+v", res)
			}
			if !l.identicalFails.shouldBlock("ctx_execute", command) || !parent.identicalFails.shouldBlock("ctx_execute", command) {
				t.Fatal("read forgave failed checks")
			}
			if res := invoke("read_zip", `{"path":"a.zip","action":"extract","target_dir":"dest"}`); res.failed || res.observation.valid {
				t.Fatalf("extract must invalidate read evidence: %+v", res)
			}
			if parent.identicalFails.shouldBlock("ctx_execute", command) {
				t.Fatal("worker extraction did not expire parent failure")
			}
			if res := invoke("ctx_execute", command); res.failed {
				t.Fatalf("check stayed blocked after repair: %+v", res)
			}
			parent.identicalFails.recordFailure("ctx_execute", command)
			parent.identicalFails.recordFailure("ctx_execute", command)
			if res := invoke("read_zip", `{"path":"a.zip","action":"extract","target_dir":"empty","pattern":"absent.txt"}`); res.failed {
				t.Fatalf("empty extraction: %+v", res)
			}
			if !parent.identicalFails.shouldBlock("ctx_execute", command) {
				t.Fatal("empty extraction forgave failed check")
			}
		})
	}
}

func TestZipReadAllArchivesParticipateInScheduling(t *testing.T) {
	root := zipScheduleArchive(t)
	if err := os.WriteFile(filepath.Join(root, "b.zip"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	l := zipScheduleLoop(t, root, tools.NewReadZip(root, 0).Spec(), tools.NewWriteFile(root).Spec())
	read := pathScheduleCall("zip", "read_zip", map[string]any{"paths": []string{"a.zip", "b.zip"}, "action": "read", "pattern": "*.txt"})
	for _, path := range []string{"a.zip", "b.zip"} {
		write := pathScheduleCall("write", "write_file", map[string]any{"path": path, "content": "changed"})
		if waves, known := l.toolConflictWaves([]llm.ToolCall{read, write}); !known || len(waves) != 2 {
			t.Fatalf("source %s lacked barrier: known=%v waves=%d", path, known, len(waves))
		}
	}
	write := pathScheduleCall("write", "write_file", map[string]any{"path": "other.txt", "content": "changed"})
	if waves, known := l.toolConflictWaves([]llm.ToolCall{read, write}); !known || len(waves) != 1 {
		t.Fatal("unrelated file serialized archive read")
	}
	for _, raw := range []string{`{"paths":[],"action":"read"}`, `{"paths":["a.zip",null],"action":"read"}`, `{"path":"a.zip","paths":["b.zip"],"action":"read"}`, `{"paths":"a.zip","action":"read"}`} {
		if _, known := fileAccessesForCall(llm.ToolCall{Name: "read_zip", Arguments: raw}); known {
			t.Fatalf("malformed sources admitted: %s", raw)
		}
	}
}

func TestZipExtractionClearsUnchangedObservationHistory(t *testing.T) {
	var progress unchangedProgress
	read := llm.ToolCall{Name: "read_zip", Arguments: `{"path":"a.zip","action":"list"}`}
	result := tools.Result{Text: "file.txt"}
	observe := func(call llm.ToolCall) {
		progress.observe([]llm.ToolCall{call}, []callOutcome{{observation: observeToolResult(call, result)}})
	}
	observe(read)
	observe(read)
	if progress.rounds != 1 {
		t.Fatal("fixture lacks repeated evidence")
	}
	observe(llm.ToolCall{Name: "read_zip", Arguments: `{"path":"a.zip","action":"extract","target_dir":"dest"}`})
	if progress.seen != nil {
		t.Fatal("extraction retained stale observation history")
	}
	observe(read)
	if progress.rounds != 0 || progress.repeats != 0 {
		t.Fatal("first post-extraction read counted as repeated")
	}
	if toolCallKind("read_zip", `{"action":"unexpected"}`) != "other" {
		t.Fatal("unknown action assumed inspection")
	}
}
