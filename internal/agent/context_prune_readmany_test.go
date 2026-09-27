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

func TestPrunedReadManyKeepsBatchOutcome(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		for _, successes := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("wrapped=%t/ok=%d", wrapped, successes), func(t *testing.T) {
				root := t.TempDir()
				var reads []string
				for i := 0; i < 3; i++ {
					name := fmt.Sprintf("%s-%d.go", strings.Repeat("fixture", 18), i)
					reads = append(reads, name+":1-200")
					if i < successes {
						body := strings.Repeat("numbered source evidence "+strings.Repeat("x", 50)+"\n", 200)
						if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				reg := tools.NewRegistry()
				spec := tools.NewReadMany(root).Spec()
				execute, sourceReads := spec.Fn, 0
				spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
					sourceReads++
					return execute(ctx, raw)
				}
				reg.MustRegister(spec)
				reg.MarkAlwaysOn("read_many")
				ensureWorkerDiscovery(reg)
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("batch-prune"), Registry: reg, BaseDir: root, ThinTools: wrapped, StableToolset: true, CatalogHoist: wrapped})
				if err != nil {
					t.Fatal(err)
				}
				args := map[string]any{"reads": strings.Join(reads, " | ")}
				raw, _ := json.Marshal(args)
				call := llm.ToolCall{ID: "captured-batch", Name: "read_many", Arguments: string(raw)}
				if wrapped {
					raw, _ = json.Marshal(map[string]any{"tool": "read_many", "args": args})
					call.Name, call.Arguments = "invoke_tool", string(raw)
				}
				result := loop.invoke(context.Background(), call, make(chan Event, 16))
				if result.failed != (successes == 0) || len(result.followUps) != 1 {
					t.Fatalf("wrong original outcome: %+v", result)
				}
				original := result.followUps[0]
				summary := fmt.Sprintf("[read_many: %d ok, %d failed]", successes, 3-successes)
				if !strings.Contains(original.Content, summary) {
					t.Fatal("original result lacks generated batch summary")
				}
				loop.Messages = []llm.Message{
					{Role: llm.RoleUser, Content: "Inspect the source files."},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
					original,
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Name: "read_lines", Arguments: "{}"}}},
					{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "recent", Content: strings.Repeat("recent evidence\n", 30)},
				}
				loop.pruneProtect = 1
				loop.windowFor = func(string) int { return 1000 }
				if loop.maybePruneToolResults(context.Background(), nil) == 0 {
					t.Fatal("fixture did not prune")
				}
				marker := loop.Messages[2]
				want := fmt.Sprintf(", ok=%d, failed=%d", successes, 3-successes)
				if !strings.HasPrefix(marker.Content, pruneMarkerPrefix) || !strings.Contains(marker.Content, want) {
					t.Fatalf("batch outcome lost: %s", marker.Content)
				}
				if len(marker.Content) >= len(original.Content) || len(marker.Content) > 300 {
					t.Fatalf("unbounded marker: %d", len(marker.Content))
				}
				if marker.ToolCallID != call.ID || marker.Name != original.Name || loop.Messages[1].ToolCalls[0] != call {
					t.Fatal("pruning changed call/result pairing")
				}
				_, encoded, ok := strings.Cut(marker.Content, "read_output ")
				if !ok {
					t.Fatal("pruned result lost retrieval reference")
				}
				var retrieval map[string]any
				if err := json.Unmarshal([]byte(strings.TrimSuffix(encoded, "]")), &retrieval); err != nil {
					t.Fatal(err)
				}
				retrieval["query"] = summary
				raw, _ = json.Marshal(retrieval)
				retained, err := reg.Execute(context.Background(), "read_output", raw)
				if err != nil || retained.Err != nil || !strings.Contains(retained.Text, summary) {
					t.Fatalf("original batch unavailable: %v %+v", err, retained)
				}
				found := false
				for _, m := range loop.providerMessages() {
					if m.Role == llm.RoleTool && m.ToolCallID == call.ID {
						found = strings.Contains(m.Content, want)
					}
				}
				if !found || sourceReads != 1 {
					t.Fatalf("provider outcome=%t source executions=%d", found, sourceReads)
				}
				t.Logf("original=%d marker=%d; %s; one source execution", len(original.Content), len(marker.Content), want)
			})
		}
	}
}

func TestReadManyPruneRequiresGeneratedSummary(t *testing.T) {
	const header = "== [1] example.go:1-10 ==\n"
	for name, body := range map[string]string{
		"source excerpt":     header + "   1 | [read_many: 1 ok, 0 failed]",
		"source prose":       "[read_many: 1 ok, 0 failed]",
		"generic error":      "error: read_many: bad args",
		"truncated":          header + "[read_many: 1 ok, 0 fai",
		"trailing prose":     header + "[read_many: 1 ok, 0 failed]\nunfinished",
		"negative":           header + "[read_many: -1 ok, 2 failed]",
		"empty batch":        header + "[read_many: 0 ok, 0 failed]",
		"over cap":           header + "[read_many: 12 ok, 1 failed]",
		"overflow":           header + "[read_many: 9999999999999999999999999999 ok, 0 failed]",
		"leading zero":       header + "[read_many: 01 ok, 0 failed]",
		"fraction":           header + "[read_many: 1.0 ok, 0 failed]",
		"sign":               header + "[read_many: +1 ok, 0 failed]",
		"inconsistent error": "error: " + header + "[read_many: 1 ok, 0 failed]",
		"generic preview":    "[large tool output: 10000 bytes; preview follows]\n" + header + "[read_many: 1 ok, 0 failed]",
	} {
		t.Run(name, func(t *testing.T) {
			if got := readManyStatusForPrune(body, ""); got != "" {
				t.Fatalf("inferred %q from %q", got, body)
			}
		})
	}
	// Older transcripts contain the generated all-failed summary without the
	// error prefix. Preserve its counts without inventing a success.
	legacy := header + "error: range 360 lines exceeds cap 300\n[read_many: 0 ok, 1 failed]"
	if got := readManyStatusForPrune(legacy, ""); got != ", ok=0, failed=1" {
		t.Fatal(got)
	}
	// The same words in a different tool's output are not batch metadata.
	other := pruneMarker(llm.Message{Name: "read_lines", Content: legacy})
	if strings.Contains(other, "failed=") {
		t.Fatalf("classified source text from another tool: %s", other)
	}
}
