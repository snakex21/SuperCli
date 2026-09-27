package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestPrunedProcessHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_PRUNE_PROCESS_HELPER") != "1" {
		return
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_PRUNE_PROCESS_READY"), 5*time.Second)
	if err != nil {
		os.Exit(99)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		os.Exit(98)
	}
	_ = conn.Close()
	size, _ := strconv.Atoi(os.Getenv("SUPERCLI_PRUNE_PROCESS_SIZE"))
	fmt.Println(strings.Repeat("process evidence\n", size))
	if os.Getenv("SUPERCLI_PRUNE_PROCESS_FAIL") == "1" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestPrunedRealProcessOutcomeSurvivesWorkerResume(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, exit := range []int{0, 7} {
			for _, lines := range []int{240, 1400} {
				t.Run(fmt.Sprintf("thin=%t/exit=%d/lines=%d", thin, exit, lines), func(t *testing.T) {
					ready, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer ready.Close()
					_ = ready.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
					root := t.TempDir()
					process := tools.NewProcessSession(root)
					defer process.Close()
					spec := process.Spec()
					execute := spec.Fn
					starts, waits := 0, 0
					spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
						var args struct{ Action string }
						_ = json.Unmarshal(raw, &args)
						if args.Action == "start" {
							starts++
						}
						if args.Action == "wait" {
							waits++
						}
						return execute(ctx, raw)
					}
					registry := tools.NewRegistry()
					registry.MustRegister(spec)
					registry.ActivateDiscovered("process_session")
					if thin {
						ensureWorkerDiscovery(registry)
					}
					provider := &stubProvider{name: "process-prune", scripts: [][]llm.Delta{{{Content: "Historical result received."}, {FinishReason: "stop"}}}}
					loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, BaseDir: root, ThinTools: thin, StableToolset: true})
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					invoke := func(id string, args map[string]any) (llm.ToolCall, toolResult) {
						raw, _ := json.Marshal(args)
						call := llm.ToolCall{ID: id, Name: "process_session", Arguments: string(raw)}
						dispatched := call
						if thin {
							raw, _ = json.Marshal(map[string]any{"tool": "process_session", "args": args})
							dispatched.Name, dispatched.Arguments = invokeToolName, string(raw)
						}
						dispatched = loop.resolveInvokeToolCalls([]llm.ToolCall{dispatched})[0]
						if dispatched.Name != "process_session" {
							t.Fatal("dispatch did not resolve")
						}
						return call, loop.invoke(ctx, dispatched, make(chan Event, 8))
					}
					fail := "0"
					if exit != 0 {
						fail = "1"
					}
					startCall, start := invoke("start", map[string]any{"action": "start", "command": []string{os.Args[0], "-test.run=^TestPrunedProcessHelper$"}, "env": []string{"SUPERCLI_PRUNE_PROCESS_HELPER=1", "SUPERCLI_PRUNE_PROCESS_READY=" + ready.Addr().String(), "SUPERCLI_PRUNE_PROCESS_SIZE=" + strconv.Itoa(lines), "SUPERCLI_PRUNE_PROCESS_FAIL=" + fail}, "yield_ms": 0})
					if start.failed || len(start.followUps) != 1 {
						t.Fatalf("start=%+v", start)
					}
					// The helper cannot emit output or exit before the parent releases it.
					// No sleeps or process-status polling are used.
					var initial struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal([]byte(start.followUps[0].Content), &initial); err != nil || initial.ID == "" {
						t.Fatalf("initial=%s err=%v", start.followUps[0].Content, err)
					}
					conn, err := ready.Accept()
					if err != nil {
						t.Fatal(err)
					}
					_, err = conn.Write([]byte{1})
					_ = conn.Close()
					if err != nil {
						t.Fatal(err)
					}
					waitCall, finished := invoke("wait", map[string]any{"action": "wait", "id": initial.ID})
					if len(finished.followUps) != 1 || finished.failed != (exit != 0) {
						t.Fatalf("wait=%+v", finished)
					}
					body := finished.followUps[0].Content
					loop.Messages = []llm.Message{
						{Role: llm.RoleUser, Content: "Check the project."},
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{startCall}}, start.followUps[0],
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{waitCall}}, finished.followUps[0],
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Name: "read_lines", Arguments: "{}"}}},
						{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "recent", Content: strings.Repeat("recent evidence ", 30)},
					}
					loop.pruneProtect = 1
					loop.windowFor = func(string) int { return 1000 }
					if loop.maybePruneToolResults(ctx, nil) == 0 {
						t.Fatal("fixture did not prune")
					}
					marker := loop.Messages[4].Content
					wantStatus := "done"
					if exit != 0 {
						wantStatus = "failed"
					}
					for _, want := range []string{pruneMarkerPrefix, "id=" + initial.ID, "status=" + wantStatus, fmt.Sprintf("exit_code=%d", exit)} {
						if !strings.Contains(marker, want) {
							t.Fatalf("missing %q after prune: %s", want, marker)
						}
					}
					if len(marker) > 240 {
						t.Fatalf("unbounded marker: %d", len(marker))
					}
					if handle := core.StoredOutputHandle(body); handle != "" {
						if !strings.Contains(marker, handle) {
							t.Fatal("output reference lost")
						}
						raw, _ := json.Marshal(map[string]any{"handle": handle})
						retained, err := registry.Execute(ctx, "read_output", raw)
						if err != nil || retained.Err != nil || !strings.Contains(retained.Text, "process evidence") {
							t.Fatalf("unreadable evidence: %v %+v", err, retained)
						}
					}
					worker := &Worker{ID: "worker-1", Agent: "code", Loop: loop, Status: "done", Runs: 1}
					if _, err := runWorkerLoop(ctx, worker, "Report the historical process result."); err != nil {
						t.Fatal(err)
					}
					if len(provider.reqs) != 1 {
						t.Fatalf("resume requests=%d", len(provider.reqs))
					}
					found := false
					for _, msg := range provider.reqs[0] {
						if msg.ToolCallID == "wait" {
							found = true
							if msg.Content != marker {
								t.Fatal("resume lost pruned status")
							}
						}
					}
					if !found || starts != 1 || waits != 1 {
						t.Fatalf("found=%t starts=%d waits=%d", found, starts, waits)
					}
					t.Logf("original model body=%d bytes, marker=%d; one start, one blocking wait, outcome retained on resume", len(body), len(marker))
				})
			}
		}
	}
}

func TestProcessPruneMarkerRecognizesOnlyOwnedMetadata(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"id":"proc-1","status":"running","stdout":"exit_code=0"}`, "id=proc-1, status=running"},
		{`{"id":"proc-2","status":"stopped","exit_code":1}`, "id=proc-2, status=stopped, exit_code=1"},
		{"error: process_session proc-3: command_failed timeout exit=124 (1.0s)\nstdout: done", "id=proc-3, status=timeout, exit_code=124"},
	} {
		if got := pruneMarker(llm.Message{Name: "process_session", Content: tc.body}); !strings.Contains(got, tc.want) {
			t.Errorf("%q => %s", tc.body, got)
		}
	}
	for _, body := range []string{
		`{"stdout":"error: process_session proc-1: command_failed exit=7 (0.0s)"}`,
		`{"id":"proc-1","status":"running","exit_code":0}`,
		`{"id":"proc-1","status":"made-up","exit_code":0}`,
		`{"id":"proc-1, exit_code=0","status":"done","exit_code":0}`,
		`{"id":"proc-1","status":"done","exit_code":0}` + "garbage",
		"log\nerror: process_session proc-1: command_failed exit=7 (0.0s)",
		"error: process_session proc-1: command_failed exit=0 (0.0s)",
		"error: process_session proc-1: command_failed timeout exit=7 (0.0s)",
	} {
		if got := pruneMarker(llm.Message{Name: "process_session", Content: body}); strings.Contains(got, "exit_code=") {
			t.Errorf("invented outcome for %q: %s", body, got)
		}
	}
	if got := pruneMarker(llm.Message{Name: "read_lines", Content: `{"id":"proc-1","status":"done","exit_code":0}`}); strings.Contains(got, "status=") {
		t.Fatal("file content became process evidence")
	}
}
