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

func TestAmbiguousRelaxedPatchFailsBothProtocolsAndAllowsExactRetry(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.go")
			const before = "first\nvalue := 1\nsecond\n    value := 1\n"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			reg.ActivateDiscovered("patch_file")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("ambiguous-edit"), Registry: reg, ThinTools: thin, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			invoke := func(id, old, newText string) (toolResult, []Event) {
				args := map[string]any{"path": "config.go", "old": old, "new": newText}
				raw, _ := json.Marshal(args)
				call := llm.ToolCall{ID: id, Name: "patch_file", Arguments: string(raw)}
				if thin {
					raw, _ = json.Marshal(map[string]any{"tool": "patch_file", "args": args})
					call.Name = "invoke_tool"
					call.Arguments = string(raw)
				}
				events := make(chan Event, 16)
				outcome := loop.invoke(context.Background(), call, events)
				close(events)
				var seen []Event
				for event := range events {
					seen = append(seen, event)
				}
				return outcome, seen
			}
			failed, events := invoke("ambiguous", "  value := 1   \n", "value := 2\n")
			if !failed.failed || len(failed.followUps) != 1 || !strings.HasPrefix(failed.followUps[0].Content, "error:") || !strings.Contains(failed.followUps[0].Content, "nothing written") {
				t.Fatalf("model did not receive rejection: %+v", failed)
			}
			results := 0
			for _, event := range events {
				if result, ok := event.(ToolResultEvent); ok {
					results++
					if result.Err == nil {
						t.Fatal("UI saw success for ambiguous edit")
					}
				}
			}
			if results != 1 {
				t.Fatalf("UI result count=%d, want 1", results)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != before {
				t.Fatalf("failed edit changed file: %q err=%v", data, err)
			}
			retry, _ := invoke("exact", "second\n    value := 1\n", "second\n    value := 2\n")
			if retry.failed {
				t.Fatalf("corrected anchor rejected: %+v", retry.followUps)
			}
			data, err = os.ReadFile(path)
			if err != nil || string(data) != "first\nvalue := 1\nsecond\n    value := 2\n" {
				t.Fatalf("retry changed wrong block: %q err=%v", data, err)
			}
		})
	}
}
