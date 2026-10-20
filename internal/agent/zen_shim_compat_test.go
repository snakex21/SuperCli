package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type zenShimTransport func(*http.Request) (*http.Response, error)

func (f zenShimTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func zenShimStub(name string, fn func(context.Context, json.RawMessage) (tools.Result, error)) tools.Tool {
	schema := `{"type":"object","properties":{"file":{"type":"string","minLength":1},"from":{"type":"integer"},"to":{"type":"integer"}},"required":["file"],"additionalProperties":false}`
	if name == "ctx_execute" {
		schema = `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["command"],"additionalProperties":false}`
	}
	return tools.Tool{Name: name, Description: "stub only", Schema: schema, ReadOnly: name == "read_lines", Fn: fn,
		Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }}
}

func TestZenShimPublicLoopFakeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, wire, target, args string
	}{
		{"chat-bash", "chat", "functions.bash", "ctx_execute", ` {"command":"synthetic, never executed"} `},
		{"chat-read", "chat", "tools.read", "read_lines", ` {"filePath":"synthetic.txt","offset":2,"limit":3} `},
		{"go-trimmed", "go", " read ", "read_lines", `{"filePath":"synthetic.txt"}`},
		{"responses-bash", "responses", "tools.bash", "ctx_execute", `{"command":"synthetic, never executed"}`},
		{"responses-read", "responses", "functions.read", "read_lines", `{"filePath":"synthetic.txt","offset":2,"limit":3}`},
		{"genuine-canonical", "chat", "functions.ctx_execute", "ctx_execute", `{"command":["synthetic","kept"]}`},
		{"ordinary-consistency", "ordinary", "functions.bash", "ctx_execute", `{"command":"synthetic, never executed"}`},
		{"bare-bash", "chat", "bash", "ctx_execute", `{"command":"synthetic, never executed"}`},
		{"bare-read", "responses", "read", "read_lines", `{"filePath":"synthetic.txt"}`},
		{"registered-bash", "chat", "functions.bash", "bash", `{"file":"synthetic.txt"}`},
		{"registered-read", "responses", "tools.read", "read", `{"file":"synthetic.txt"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			var executed atomic.Int32
			var received string
			reg.MustRegister(zenShimStub(tc.target, func(_ context.Context, args json.RawMessage) (tools.Result, error) {
				executed.Add(1)
				received = string(args)
				return tools.Result{Text: "synthetic success"}, nil
			}))
			reg.Activate(tc.target)
			if tc.target == "bash" || tc.target == "read" {
				canonical := "ctx_execute"
				if tc.target == "read" {
					canonical = "read_lines"
				}
				reg.MustRegister(zenShimStub(canonical, func(context.Context, json.RawMessage) (tools.Result, error) {
					t.Error("registered real alias was hijacked")
					return tools.Result{}, nil
				}))
				reg.Activate(canonical)
			}
			base := "https://opencode.ai/zen/v1"
			if tc.protocol == "go" {
				base = "https://opencode.ai/zen/go/v1"
			}
			if tc.protocol == "ordinary" {
				base = "https://fixture.invalid/v1"
			}
			var requests atomic.Int32
			client := &http.Client{Transport: zenShimTransport(func(r *http.Request) (*http.Response, error) {
				n := requests.Add(1)
				if n > 2 {
					return nil, fmt.Errorf("unexpected retry %d", n)
				}
				var req map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					return nil, err
				}
				var defs []map[string]json.RawMessage
				if err := json.Unmarshal(req["tools"], &defs); err != nil {
					return nil, err
				}
				names := map[string]bool{}
				for _, def := range defs {
					if tc.protocol != "responses" {
						var function map[string]json.RawMessage
						if err := json.Unmarshal(def["function"], &function); err != nil {
							return nil, err
						}
						def = function
					}
					var name string
					if err := json.Unmarshal(def["name"], &name); err != nil {
						return nil, err
					}
					names[name] = true
				}
				if !names[tc.target] || (tc.protocol != "ordinary" && (!names["bash"] || !names["read"])) {
					return nil, fmt.Errorf("required wire tools lost: %v", names)
				}
				var body string
				if tc.protocol == "responses" {
					response := map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}}
					if n == 1 {
						response["response"] = map[string]any{"status": "completed", "output": []any{map[string]any{"type": "function_call", "id": "item-shim", "call_id": "shim-id", "name": tc.wire, "arguments": tc.args}}}
					} else {
						body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture complete\"}\n\n"
					}
					raw, _ := json.Marshal(response)
					body += "data: " + string(raw) + "\n\n"
				} else {
					choice := map[string]any{"delta": map[string]any{"content": "fixture complete"}, "finish_reason": "stop"}
					if n == 1 {
						choice = map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "shim-id", "type": "function", "function": map[string]any{"name": tc.wire, "arguments": tc.args}}}}, "finish_reason": "tool_calls"}
					}
					raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
					body = "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			var provider llm.Provider
			var err error
			if tc.protocol == "responses" {
				provider, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: base, Model: "synthetic-shim", HTTPClient: client})
			} else {
				provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: "synthetic-shim", HTTPClient: client})
			}
			if err != nil {
				t.Fatal(err)
			}
			writer := &recordingWriter{}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: writer, System: "synthetic fixture", MaxSteps: 3, SkipImplementationHint: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ch, err := loop.Run(ctx, "run the synthetic fixture")
			if err != nil {
				t.Fatal(err)
			}
			starts, ends := 0, 0
			for ev := range ch {
				switch ev := ev.(type) {
				case ToolCallEvent:
					starts++
					if ev.Name != tc.target || ev.ID != "shim-id" {
						t.Errorf("call=%+v", ev)
					}
				case ToolResultEvent:
					ends++
					if ev.Err != nil || ev.ID != "shim-id" {
						t.Errorf("result=%+v", ev)
					}
				case ErrorEvent:
					t.Error(ev.Err)
				}
			}
			if executed.Load() != 1 || requests.Load() != 2 || starts != 1 || ends != 1 {
				t.Fatalf("executions=%d requests=%d starts=%d ends=%d", executed.Load(), requests.Load(), starts, ends)
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(received), &args); err != nil {
				t.Fatal(err)
			}
			if tc.target == "ctx_execute" {
				argv, ok := args["command"].([]any)
				if !ok || len(argv) == 0 {
					t.Fatalf("argv=%s", received)
				}
				if tc.wire == "functions.ctx_execute" {
					if !reflect.DeepEqual(argv, []any{"synthetic", "kept"}) {
						t.Fatalf("canonical args changed: %s", received)
					}
				} else if len(argv) != 3 || argv[2] != "synthetic, never executed" {
					t.Fatalf("adapter skipped: %s", received)
				}
			} else if args["file"] != "synthetic.txt" {
				t.Fatalf("read adapter skipped: %s", received)
			}
			for _, history := range [][]llm.Message{loop.Messages, writer.messages} {
				var assistant *llm.ToolCall
				results := 0
				for _, m := range history {
					for _, c := range m.ToolCalls {
						if c.ID == "shim-id" {
							copy := c
							assistant = &copy
						}
					}
					if m.Role == llm.RoleTool && m.ToolCallID == "shim-id" {
						results++
						if assistant == nil || m.Name != assistant.Name || assistant.Arguments != strings.TrimSpace(tc.args) {
							t.Errorf("replay pair/raw args mismatch: assistant=%+v result=%+v", assistant, m)
						}
					}
				}
				if assistant == nil || results != 1 {
					t.Fatalf("missing replay pair: %+v", history)
				}
			}
		})
	}
}

func TestZenShimNormalizationGuards(t *testing.T) {
	for _, scenario := range []string{"missing", "inactive", "restricted", "no-defs", "registered-bash", "registered-read", "unknown-namespace", "double-wrapper", "typo"} {
		t.Run(scenario, func(t *testing.T) {
			reg := tools.NewRegistry()
			stub := func(context.Context, json.RawMessage) (tools.Result, error) {
				t.Fatal("must not execute")
				return tools.Result{}, nil
			}
			reg.MustRegister(zenShimStub("ctx_execute", stub))
			reg.MustRegister(zenShimStub("read_lines", stub))
			if scenario != "inactive" {
				reg.Activate("ctx_execute", "read_lines")
			}
			if scenario == "missing" {
				reg = tools.NewRegistry()
			}
			if scenario == "restricted" {
				reg = restrictedRegistry(reg, []string{"read_lines"})
			}
			wire := "functions.bash"
			want := "invalid_tool_call"
			switch scenario {
			case "registered-bash", "registered-read":
				name := "bash"
				if scenario == "registered-read" {
					name = "read"
				}
				tool := zenShimStub(name, stub)
				tool.Schema = `{"type":"object"}`
				reg.MustRegister(tool)
				reg.Activate(name)
				wire = name
				want = name
			case "unknown-namespace":
				wire = "other.bash"
			case "double-wrapper":
				wire = "functions.tools.bash"
			case "typo":
				wire = "functions.bas"
			}
			loop := &Loop{registry: reg, route: RouteCoordinator, finalReplyOnly: scenario == "no-defs"}
			original := llm.ToolCall{ID: "guard-id", Name: wire, Arguments: ` {"command":"never run"} `}
			call := loop.normalizeToolCallNames([]llm.ToolCall{original})[0]
			if call.Name != want || call.ID != original.ID || call.Arguments != original.Arguments {
				t.Fatalf("normalized=%+v want=%s", call, want)
			}
			if strings.HasPrefix(scenario, "registered-") {
				out := make(chan Event, 8)
				// A cancelled dispatch still exercises placeholder precedence without running the registered tool.
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				result := loop.invoke(ctx, call, out)
				if len(result.followUps) != 1 || result.followUps[0].Name != want {
					t.Fatalf("registered precedence lost: %+v", result)
				}
			} else {
				out := make(chan Event, 8)
				result := loop.invoke(context.Background(), call, out)
				if !result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, badToolCallMarker) {
					t.Fatalf("guard did not refuse dispatch: %+v", result)
				}
			}
		})
	}
}

func TestZenShimBadArgsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, wire, args string
		cancel           bool
	}{
		{"missing-command", "functions.bash", `{}`, false},
		{"missing-path", "tools.read", `{}`, false},
		{"invalid-command", "functions.bash", `{"command":{"bad":true}}`, false},
		{"unrepairable", "functions.bash", `{"command":`, false},
		{"cancel", "functions.bash", `{"command":"never run"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			var executed atomic.Int32
			for _, name := range []string{"ctx_execute", "read_lines"} {
				reg.MustRegister(zenShimStub(name, func(context.Context, json.RawMessage) (tools.Result, error) {
					executed.Add(1)
					return tools.Result{Text: "unexpected"}, nil
				}))
			}
			reg.Activate("ctx_execute", "read_lines")
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			calls := loop.normalizeToolCallNames([]llm.ToolCall{{ID: "bad-id", Name: tc.wire, Arguments: tc.args}})
			out := make(chan Event, 16)
			ok, outcomes := loop.invokeToolCalls(ctx, calls, out)
			if !ok || len(outcomes) != 1 || executed.Load() != 0 {
				t.Fatalf("ok=%v outcomes=%+v executed=%d", ok, outcomes, executed.Load())
			}
			close(out)
			failures := 0
			for ev := range out {
				if ev, ok := ev.(ToolResultEvent); ok && ev.Err != nil {
					failures++
				}
			}
			if failures != 1 {
				t.Fatalf("error events=%d", failures)
			}
		})
	}
}
