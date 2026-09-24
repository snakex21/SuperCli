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

func TestAtomicPatchRecoveryReachesBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "settings.txt")
			const original = "first=1\nsecond=2\nthird=3\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			spec := tools.NewPatchFile(home).Spec()
			execute := spec.Fn
			calls := 0
			var afterFailure string
			spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				calls++
				result, err := execute(ctx, args)
				if calls == 1 {
					b, _ := os.ReadFile(path)
					afterFailure = string(b)
				}
				return result, err
			}
			reg.MustRegister(spec)
			reg.MarkAlwaysOn("patch_file")
			changes := []map[string]string{{"old": "first=1", "new": "first=10"}, {"old": "second=999", "new": "second=20"}, {"old": "third=3", "new": "third=30"}}
			arguments := func() string {
				raw, _ := json.Marshal(map[string]any{"path": "settings.txt", "changes": changes})
				return string(raw)
			}
			first := arguments()
			changes[1]["old"] = "second=2"
			fixed := arguments()
			call := func(id, args string) []llm.Delta {
				if !thin {
					return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: "patch_file", Arguments: args}}}
				}
				var params map[string]json.RawMessage
				if err := json.Unmarshal([]byte(args), &params); err != nil {
					t.Fatal(err)
				}
				return []llm.Delta{{Content: "«patch_file\npath: settings.txt\nchanges: " + string(params["changes"]) + "»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "patch-recovery", scripts: [][]llm.Delta{call("failed", first), call("corrected", fixed), {{Content: "All three settings updated.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, mustRun(t, loop, "Update all three settings."))
			for _, event := range events {
				if problem, ok := event.(ErrorEvent); ok {
					t.Fatal(problem.Err)
				}
			}
			if calls != 2 || provider.calls != 3 {
				t.Fatalf("tool calls=%d model calls=%d", calls, provider.calls)
			}
			if afterFailure != original {
				t.Fatalf("rejected batch partially applied: %q", afterFailure)
			}
			var failure string
			for _, message := range provider.reqs[1] {
				if message.Role == llm.RoleTool && message.Name == "patch_file" {
					failure = message.Content
				}
			}
			if !strings.Contains(failure, "nothing written") || !strings.Contains(failure, "fix change 1 and resend all 3 changes") || strings.Contains(failure, "alone") {
				t.Fatalf("model saw wrong recovery: %s", failure)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "first=10\nsecond=20\nthird=30\n" {
				t.Fatalf("retry lost changes: %q %v", got, err)
			}
		})
	}
}
