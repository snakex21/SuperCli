package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestPatchRedundantPathNeedsNoRepairTurn(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, copied := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/copied=%t", thin, copied), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "config.go")
				if err := os.WriteFile(path, []byte("package config\nconst Value = 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
				reg := tools.NewRegistry()
				spec := tools.NewPatchFile(root).Spec()
				execute := spec.Fn
				executions := 0
				spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					executions++
					return execute(ctx, args)
				}
				reg.MustRegister(spec)
				if copied {
					child := tools.NewRegistry()
					if err := child.RegisterFrom(reg, "patch_file"); err != nil {
						t.Fatal(err)
					}
					reg = child
				}
				reg.ActivateDiscovered("patch_file")
				args := "{\"path\":\"config.go\",\"changes\":[{\"path\":\"config.go\",\"old\":\"Value = 1\",\"new\":\"Value = 2\"}]}"
				call := llm.ToolCall{ID: "patch", Name: "patch_file", Arguments: args}
				if thin {
					ensureWorkerDiscovery(reg)
					call.Name = "invoke_tool"
					call.Arguments = "{\"tool\":\"patch_file\",\"args\":" + args + "}"
				}
				provider := &stubProvider{name: "patch-path", scripts: [][]llm.Delta{{{ToolCall: &call, FinishReason: "tool_calls"}}, {{Content: "Updated config.go.", FinishReason: "stop"}}}}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, BaseDir: root})
				if err != nil {
					t.Fatal(err)
				}
				for _, ev := range drainEvents(t, mustRun(t, loop, "Change Value from 1 to 2.")) {
					if e, ok := ev.(ToolResultEvent); ok && e.Err != nil {
						t.Fatalf("repair turn required: %v", e.Err)
					}
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != "package config\nconst Value = 2\n" || executions != 1 || len(provider.reqs) != 2 {
					t.Fatalf("content=%q executions=%d requests=%d", got, executions, len(provider.reqs))
				}
			})
		}
	}
}

func TestPatchRedundantPathSavedReplay(t *testing.T) {
	file := os.Getenv("SUPERCLI_PATCH_REDUNDANT_PATH_REPLAY")
	if file == "" {
		t.Skip("opt-in private saved-argument replay")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Bad   json.RawMessage
		Retry json.RawMessage
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	var args struct {
		Path    string
		Changes []struct{ Old, New string }
	}
	if err := json.Unmarshal(capture.Bad, &args); err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(args.Path) || strings.HasPrefix(filepath.Clean(args.Path), "..") || len(args.Changes) != 1 {
		t.Fatal("unsupported fixture path/shape")
	}
	root := t.TempDir()
	target := filepath.Join(root, args.Path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(args.Changes[0].Old), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewPatchFile(root).Spec())
	result, err := reg.Execute(context.Background(), "patch_file", capture.Bad)
	if err != nil || result.Err != nil {
		t.Fatalf("saved call still needs retry: %v %v", err, result.Err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != args.Changes[0].New {
		t.Fatal("saved replacement changed")
	}
	t.Log("saved first request executes the same replacement without its formatting-only retry")
}
