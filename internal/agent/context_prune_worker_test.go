package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestPrunedWorkerCanContinueUsingRetainedEvidence(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/failed=%t", thin, failed), func(t *testing.T) {
				reads := 0
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read fixture", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					reads++
					return tools.Result{Text: "const RetryLimit = 731"}, nil
				}})
				reg.MarkAlwaysOn("read_lines")
				report := strings.Repeat("Existing findings from the source.\n", 100)
				final := []llm.Delta{{Content: report}, {FinishReason: "stop"}}
				if failed {
					final = []llm.Delta{{Content: report}, {Err: errors.New("provider stream interrupted")}}
				}
				provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "read-1", Name: "read_lines", Arguments: "{}"}}, {FinishReason: "tool_calls"}}, final,
					{{Content: "RetryLimit is 731; follow-up complete."}, {FinishReason: "stop"}},
				}}
				parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, StableToolset: true, CatalogHoist: true})
				if err != nil {
					t.Fatal(err)
				}
				specs := NewSubAgentRegistry()
				MustRegisterAll(specs, BuiltinSubAgents())
				task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				reg.MustRegister(task.Spec())
				reg.MustRegister(NewSendMessageTool(task.Workers).Spec())
				if thin {
					ensureWorkerDiscovery(reg)
				}
				call := llm.ToolCall{ID: "delegation", Name: "task", Arguments: `{"agent":"review","prompt":"Inspect the retry limit."}`}
				result := parent.invoke(context.Background(), call, make(chan Event, 64))
				if len(result.followUps) != 1 || result.failed != failed {
					t.Fatalf("task=%+v", result)
				}
				before := result.followUps[0].Content
				parent.Messages = []llm.Message{
					{Role: llm.RoleUser, Content: "Review the retry limit."}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, result.followUps[0],
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Name: "read_lines", Arguments: "{}"}}},
					{Role: llm.RoleTool, ToolCallID: "recent", Name: "read_lines", Content: strings.Repeat("recent evidence ", 30)},
				}
				parent.pruneProtect = 1
				parent.windowFor = func(string) int { return 1000 }
				if parent.maybePruneToolResults(context.Background(), nil) == 0 {
					t.Fatal("fixture did not prune")
				}
				marker := parent.Messages[2].Content
				status := "done"
				if failed {
					status = "failed"
				}
				if !strings.HasPrefix(marker, pruneMarkerPrefix) || !strings.Contains(marker, "worker_id=worker-1") || !strings.Contains(marker, "status="+status) {
					t.Fatalf("worker identity lost: %s", marker)
				}
				if len(marker) > 220 {
					t.Fatalf("unbounded marker: %d", len(marker))
				}
				var visible string
				for _, msg := range parent.providerMessages() {
					if msg.ToolCallID == call.ID {
						visible = msg.Content
					}
				}
				if visible != marker {
					t.Fatal("provider view lost worker metadata")
				}
				if handle := core.StoredOutputHandle(before); handle != "" && !strings.Contains(marker, handle) {
					t.Fatal("full report reference lost")
				}
				// Use only the ID supplied to the model after pruning, not a registry list.
				_, tail, _ := strings.Cut(marker, "worker_id=")
				id, _, _ := strings.Cut(tail, ",")
				args := map[string]any{"to": id, "message": "Continue from the existing source evidence."}
				raw, _ := json.Marshal(args)
				follow := llm.ToolCall{ID: "continue", Name: "send_message", Arguments: string(raw)}
				if thin {
					raw, _ = json.Marshal(map[string]any{"tool": "send_message", "args": args})
					follow.Name, follow.Arguments = invokeToolName, string(raw)
					follow = parent.resolveInvokeToolCalls([]llm.ToolCall{follow})[0]
					if follow.Name != "send_message" {
						t.Fatal("known worker still needs tool discovery")
					}
				}
				resumed := parent.invoke(context.Background(), follow, make(chan Event, 64))
				if resumed.failed || len(resumed.followUps) != 1 || !strings.Contains(resumed.followUps[0].Content, "RetryLimit is 731") {
					t.Fatalf("resume=%+v", resumed)
				}
				if reads != 1 || len(task.Workers.List()) != 1 || provider.calls != 3 {
					t.Fatalf("work restarted: reads=%d workers=%d calls=%d", reads, len(task.Workers.List()), provider.calls)
				}
				found := false
				for _, msg := range provider.reqs[2] {
					if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "RetryLimit = 731") {
						found = true
					}
				}
				if !found {
					t.Fatal("worker continuation lost gathered evidence")
				}
				t.Logf("task result %d -> %d bytes; one worker, one source read, direct continuation", len(before), len(marker))
			})
		}
	}
}

func TestWorkerPruneIdentityEnvelopeVariants(t *testing.T) {
	for _, status := range []string{"done", "failed", "stopped", "running"} {
		for _, shape := range []string{"plain", "large", "outline"} {
			t.Run(status+"/"+shape, func(t *testing.T) {
				report := "findings\n" + strings.Repeat("body\n", 100)
				if shape != "plain" {
					report = strings.Repeat("large report\n", 2000)
				}
				if shape == "outline" {
					report = "## One\n" + report + "\n## Two\n" + report + "\n## Three\n" + report
				}
				worker := &Worker{ID: "worker-42", Agent: "review", Status: status, Loop: &Loop{}}
				var cause error
				if status == "failed" || status == "stopped" {
					cause = errors.New("interrupted")
				}
				result := workerResult(worker, report, cause)
				store := core.NewOutputStore()
				for _, name := range []string{"task", "send_message"} {
					body := store.ModelContent(name, result)
					marker := pruneMarker(llm.Message{Name: name, Content: body})
					if !strings.Contains(marker, "worker_id=worker-42, worker_status="+status) {
						t.Fatalf("metadata lost: %s", marker)
					}
					if strings.Contains(marker, "send_message to") || strings.Contains(marker, "verified") {
						t.Fatal("pruning invented retry guidance or verification")
					}
					if len(marker) > 220 {
						t.Fatalf("unbounded marker: %d", len(marker))
					}
				}
			})
		}
	}
}

func TestWorkerPruneDoesNotInventIdentityFromReportBody(t *testing.T) {
	valid := renderWorkerNotification(&Worker{ID: "worker-1", Agent: "review", Status: "done"}, "report")
	for _, body := range []string{
		"log prefix\n" + valid,
		"error: unrecognized failure\n" + valid,
		strings.Replace(valid, "<task-id>worker-1</task-id>", "<task-id>worker-1, status=done</task-id>", 1),
		strings.Replace(valid, "<status>done</status>", "<status>invented</status>", 1),
		strings.Replace(valid, "<task-id>worker-1</task-id>", "<task-id>worker-00</task-id>", 1),
		"<task-notification>\n<task-id>worker-2</task-id>\n<agent>review\n</agent>\n<status>done</status>\n",
		"<task-notification>\n<task-id>worker-2</task-id>\n<result>" + valid,
		"error: worker worker-1 (review), status=arbitrary; call failed: error",
		"[large tool output: invalid; preview follows; handle=out_abcdef]\n" + valid,
	} {
		if got := pruneMarker(llm.Message{Name: "task", Content: body}); strings.Contains(got, "worker_id=") {
			t.Errorf("invented worker for %q: %s", body, got)
		}
	}
	if got := pruneMarker(llm.Message{Name: "read_lines", Content: valid}); strings.Contains(got, "worker_id=") {
		t.Fatal("file text became task metadata")
	}
}

func TestSavedWorkerPruneIdentityReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_WORKER_PRUNE_REPLAY")
	if path == "" {
		t.Skip("set SUPERCLI_WORKER_PRUNE_REPLAY to frozen saved-reports.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Seq     int    `json:"seq"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatalf("expected four known reports, got %d", len(cases))
	}
	ids := map[int]string{887: "worker-1", 889: "worker-1", 949: "worker-2", 1371: "worker-3"}
	for _, tc := range cases {
		marker := pruneMarker(llm.Message{Name: tc.Name, Content: tc.Content})
		status := "done"
		if tc.Seq == 1371 {
			status = "failed"
		}
		if !strings.Contains(marker, "worker_id="+ids[tc.Seq]+", worker_status="+status) {
			t.Errorf("seq=%d missing identity: %s", tc.Seq, marker)
		}
		if handle := core.StoredOutputHandle(tc.Content); handle == "" || !strings.Contains(marker, handle) {
			t.Errorf("seq=%d missing original handle", tc.Seq)
		}
		t.Logf("seq=%d %d -> %d bytes: %s", tc.Seq, len(tc.Content), len(marker), marker)
	}
}
