package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestRequestedEmptyFileMutationDoesNotBecomeFailedWork(t *testing.T) {
	for _, tc := range []struct {
		name, initial, tool, args string
	}{
		{"create marker", "", "create_file", `{"path":".gitkeep","content":""}`},
		{"delete all", "remove me\n", "patch_file", `{"path":".gitkeep","old":"remove me\n","new":""}`},
		{"delete CRLF", "zażółć\r\n", "patch_file", `{"path":".gitkeep","old":"zażółć\n","new":""}`},
		{"batch delete", "first\nsecond\n", "patch_file", `{"path":".gitkeep","changes":[{"old":"first\n","new":""},{"old":"second\n","new":""}]}`},
		{"chained delete", "old\n", "patch_file", `{"path":".gitkeep","changes":[{"old":"old\n","new":"replacement\n"},{"old":"replacement\n","new":""}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".gitkeep")
			if tc.tool == "patch_file" {
				if err := os.WriteFile(path, []byte(tc.initial), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewCreateFile(root).Spec())
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			completions := 0
			reg.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				completions++
				return tools.Result{Text: "done"}, nil
			}})
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "mutation", Name: tc.tool, Arguments: tc.args}
			result := loop.invoke(context.Background(), call, make(chan Event, 8))
			actual, err := os.ReadFile(path)
			if err != nil || len(actual) != 0 {
				t.Fatalf("requested empty file missing: %q %v", actual, err)
			}
			if result.failed || loop.concreteFailure.Load() || loop.identicalFails.attempts(call.Name, call.Arguments) != 0 {
				t.Fatalf("completed mutation was mislabeled as failed: %+v", result)
			}
			completed := loop.invoke(context.Background(), llm.ToolCall{ID: "goal", Name: "goal", Arguments: `{"action":"complete_task"}`}, make(chan Event, 8))
			if completed.failed || completions != 1 {
				t.Fatalf("empty file forced more work: %+v", completed)
			}
		})
	}
}
