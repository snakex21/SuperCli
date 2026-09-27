package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Exercise send_message through the real serializers and SSE decoders. A
// registry/history inspection alone cannot establish what the endpoint receives.
func TestWorkerContinuationPreservesWireEvidence(t *testing.T) {
	priorDiscard := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(priorDiscard) })
	for _, protocol := range []string{"chat", "zen-responses"} {
		for _, mode := range []string{"native", "thin-tail", "thin-hoist", "profile-default"} {
			for _, discard := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/discard=%t", protocol, mode, discard), func(t *testing.T) {
					llm.SetDiscardPreviousReasoning(discard)
					const callID = "call_fixture_read"
					const evidence = "const VerifiedFixtureValue = 417"
					const initial = "Inspect fixture.go with read_lines and report the value."
					const followup = "Continue using the previously read value and report it."
					const report = "The recorded fixture value is 417."
					const activeReasoning = "fixture-tool-continuation-state"
					const finalReasoning = "fixture-completed-reply-state"
					type request struct {
						body                  map[string]json.RawMessage
						path, client, session string
					}
					captured := make(chan request, 4)
					var calls atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						n := calls.Add(1)
						if n > 3 {
							http.Error(w, "unexpected extra request", http.StatusBadRequest)
							return
						}
						captured <- request{body, r.URL.Path, r.Header.Get("X-OpenCode-Client"), r.Header.Get("X-OpenCode-Session")}
						w.Header().Set("Content-Type", "text/event-stream")
						emit := func(v any) {
							b, _ := json.Marshal(v)
							fmt.Fprintf(w, "data: %s\n\n", b)
						}
						reasoning := activeReasoning
						if n != 1 {
							reasoning = finalReasoning
						}
						if protocol == "chat" {
							delta := map[string]any{"reasoning_content": reasoning}
							finish := "stop"
							if n == 1 {
								delta["tool_calls"] = []any{map[string]any{
									"index": 0, "id": callID, "type": "function",
									"function": map[string]any{"name": "read_lines", "arguments": "{\"file\":\"fixture.go\",\"from\":1,\"to\":2}"},
								}}
								finish = "tool_calls"
							} else {
								delta["content"] = report
							}
							emit(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
							fmt.Fprint(w, "data: [DONE]\n\n")
						} else {
							emit(map[string]any{"type": "response.output_item.done", "item": map[string]any{
								"type": "reasoning", "id": fmt.Sprintf("rs_fixture_%d", n),
								"encrypted_content": reasoning, "summary": []any{},
							}})
							if n == 1 {
								emit(map[string]any{"type": "response.output_item.done", "item": map[string]any{
									"type": "function_call", "call_id": callID, "name": "read_lines",
									"arguments": "{\"file\":\"fixture.go\",\"from\":1,\"to\":2}",
								}})
							} else {
								emit(map[string]any{"type": "response.output_text.delta", "delta": report})
							}
							emit(map[string]any{"type": "response.completed", "response": map[string]any{}})
						}
					}))
					defer srv.Close()
					model := "qwen3.8-27b-uncensored"
					if protocol == "zen-responses" {
						model = "muse-spark-1.2-contributor-free"
					}
					caps := llm.NewCapabilityRegistry()
					caps.Register(llm.ModelInfo{ID: model, ToolUse: true, Reasoning: true, ReasoningKnown: true, Source: llm.SourceProvider})
					var provider llm.Provider
					var err error
					if protocol == "chat" {
						provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: model, Capabilities: caps})
					} else {
						provider, err = llm.NewResponses(llm.ResponsesConfig{
							BaseURL: "https://opencode.ai/zen/v1", Model: model, Capabilities: caps,
							HTTPClient: &http.Client{Transport: workerRequestTransport(func(req *http.Request) (*http.Response, error) {
								clone := req.Clone(req.Context())
								clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
								clone.Host = clone.URL.Host
								return http.DefaultTransport.RoundTrip(clone)
							})},
						})
					}
					if err != nil {
						t.Fatal(err)
					}
					root := t.TempDir()
					if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\n"+evidence+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
					source, base := workerSchemaFixture(t), tools.NewRegistry()
					for _, name := range []string{"patch_file", "create_file", "ctx_execute", "read_docx", "edit_docx"} {
						if err := base.RegisterFrom(source, name); err != nil {
							t.Fatal(err)
						}
					}
					base.MustRegister(tools.NewReadLines(root).Spec())
					base.MustRegister(tools.NewToolSearcher(base, nil).Spec())
					cfg := LoopConfig{Provider: provider, Caps: caps, Registry: base, BaseDir: root,
						ThinTools: mode != "native", StableToolset: true, CatalogHoist: mode == "thin-hoist"}
					if mode == "profile-default" {
						baseURL := srv.URL + "/v1"
						if protocol == "zen-responses" {
							baseURL = "https://opencode.ai/zen/v1"
						}
						profile, err := continuationExecutionProfile(baseURL, model, caps, "", "")
						if err != nil {
							t.Fatal(err)
						}
						if !profile.ThinTools || !profile.StableToolset || !profile.CatalogHoist {
							t.Fatalf("expected stable hoisted default for %s: %+v", model, profile)
						}
						cfg.ThinTools, cfg.StableToolset, cfg.CatalogHoist = profile.ThinTools, profile.StableToolset, profile.CatalogHoist
					}
					parent, err := NewLoop(cfg)
					if err != nil {
						t.Fatal(err)
					}
					specs := NewSubAgentRegistry()
					MustRegisterAll(specs, BuiltinSubAgents())
					newLoops := 0
					task, err := NewAgentTool(specs, parent, base, provider, caps, func(cfg LoopConfig) (*Loop, error) {
						newLoops++
						if mode == "profile-default" && (!cfg.ThinTools || !cfg.StableToolset || !cfg.CatalogHoist) {
							t.Fatal("worker lost default tool/cache profile")
						}
						return NewLoop(cfg)
					})
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					const sessionID = "ses_0123456789abcdef0123456789"
					ctx = llm.WithOpenCodeSession(ctx, sessionID)
					args, _ := json.Marshal(map[string]any{"agent": "code", "prompt": initial})
					result, err := task.execute(ctx, args)
					if err != nil || result.Err != nil {
						t.Fatalf("initial worker: %v %v", err, result.Err)
					}
					workers := task.Workers.List()
					if len(workers) != 1 {
						t.Fatalf("workers=%d", len(workers))
					}
					args, _ = json.Marshal(map[string]any{"to": workers[0].ID, "message": followup})
					result, err = NewSendMessageTool(task.Workers).execute(ctx, args)
					if err != nil || result.Err != nil || !strings.Contains(result.Text, report) {
						t.Fatalf("continuation: %v %v %s", err, result.Err, result.Text)
					}
					if newLoops != 1 || len(captured) != 3 {
						t.Fatalf("loops=%d requests=%d; want one loop and three requests", newLoops, len(captured))
					}
					first, afterTool, resumed := <-captured, <-captured, <-captured
					var originalDefs, resumedDefs any
					if err := json.Unmarshal(first.body["tools"], &originalDefs); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(resumed.body["tools"], &resumedDefs); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(originalDefs, resumedDefs) {
						t.Fatal("tool definitions changed on continuation")
					}
					for _, name := range []string{"patch_file", "create_file", "read_lines", "ctx_execute", "tool_search", "read_output"} {
						if !strings.Contains(string(resumed.body["tools"]), "\""+name+"\"") {
							t.Errorf("%s absent on continuation", name)
						}
					}
					field := "messages"
					if protocol == "zen-responses" {
						field = "input"
						if resumed.path != "/zen/v1/responses" || resumed.client != "cli" || resumed.session != sessionID {
							t.Fatalf("Zen route/headers changed: %s %s %s", resumed.path, resumed.client, resumed.session)
						}
					}
					for _, got := range []request{afterTool, resumed} {
						// Wire names use underscores; decode them explicitly below.
						var rawItems []map[string]json.RawMessage
						if err := json.Unmarshal(got.body[field], &rawItems); err != nil {
							t.Fatal(err)
						}
						pairCall, pairResult := false, false
						for _, item := range rawItems {
							var role, kind, id, output string
							_ = json.Unmarshal(item["role"], &role)
							_ = json.Unmarshal(item["type"], &kind)
							if protocol == "chat" {
								var toolCalls []struct{ ID string }
								_ = json.Unmarshal(item["tool_calls"], &toolCalls)
								for _, tc := range toolCalls {
									pairCall = pairCall || (role == "assistant" && tc.ID == callID)
								}
								_ = json.Unmarshal(item["tool_call_id"], &id)
								_ = json.Unmarshal(item["content"], &output)
								pairResult = pairResult || (role == "tool" && id == callID && strings.Contains(output, evidence))
							} else {
								_ = json.Unmarshal(item["call_id"], &id)
								_ = json.Unmarshal(item["output"], &output)
								pairCall = pairCall || (kind == "function_call" && id == callID)
								pairResult = pairResult || (kind == "function_call_output" && id == callID && strings.Contains(output, evidence))
							}
						}
						if !pairCall || !pairResult || !strings.Contains(string(got.body[field]), activeReasoning) {
							t.Fatalf("lost call/result/native continuation state: call=%t result=%t", pairCall, pairResult)
						}
					}
					history := string(resumed.body[field])
					for _, text := range []string{initial, followup, report} {
						if !strings.Contains(history, text) {
							t.Errorf("continuation lost %q", text)
						}
					}
					if strings.Contains(history, finalReasoning) == discard {
						t.Errorf("completed-reply reasoning policy not reflected on wire (discard=%t)", discard)
					}
				})
			}
		}
	}
}
