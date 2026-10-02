package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const discoveryWireSchema = `{"type":"object","properties":{"mode":{"type":"string","enum":["two  spaces","other"],"default":"two  spaces"},"count":{"type":"integer","minimum":1,"maximum":3,"default":2},"options":{"type":"object","properties":{"enabled":{"type":"boolean","default":false}},"required":["enabled"],"additionalProperties":false}},"required":["mode","count"],"additionalProperties":false,"examples":[9007199254740993,1e+09,-0]}`

type discoveryWireMeasure struct {
	Requests, Bytes, DiscoveryBytes, Executions int
	Quality                                     bool
}

// Exercise real Chat Completions serialization and the production loop with a
// local deterministic transport. This measures payload bytes, not model tokens
// or provider latency, and verifies dispatch after both discovery call forms.
func TestToolSearchModelTextWire(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("envelope=%v", wrapped), func(t *testing.T) {
			before := runDiscoveryWire(t, wrapped, false)
			after := runDiscoveryWire(t, wrapped, true)
			if !before.Quality || !after.Quality || before.Requests != 3 || after.Requests != 3 || before.Executions != 1 || after.Executions != 1 {
				t.Fatalf("dispatch or quality changed: before=%+v after=%+v", before, after)
			}
			if after.Bytes >= before.Bytes || after.DiscoveryBytes >= before.DiscoveryBytes {
				t.Fatalf("no real serialized payload saving: before=%+v after=%+v", before, after)
			}
			t.Logf("requests=%d executions=%d quality=%v discovery_bytes=%d->%d cumulative_http_bytes=%d->%d saved=%d", after.Requests, after.Executions, after.Quality, before.DiscoveryBytes, after.DiscoveryBytes, before.Bytes, after.Bytes, before.Bytes-after.Bytes)
		})
	}
}

func runDiscoveryWire(t *testing.T, wrapped, projected bool) discoveryWireMeasure {
	t.Helper()
	var requests atomic.Int32
	captured := make(chan []byte, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 128*1024))
		if err != nil {
			http.Error(w, "fixture request read failure", http.StatusBadRequest)
			return
		}
		captured <- body
		step := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		name, args, id := "", "", ""
		switch step {
		case 1:
			name, args, id = "tool_search", `{"query":"check_contract"}`, "discover"
			if wrapped {
				name, args = "invoke_tool", `{"tool":"tool_search","args":{"query":"check_contract"}}`
			}
		case 2:
			name, args, id = "check_contract", `{"mode":"two  spaces","count":2,"options":{"enabled":false}}`, "check"
			if wrapped {
				name, args = "invoke_tool", `{"tool":"check_contract","args":{"mode":"two  spaces","count":2,"options":{"enabled":false}}}`
			}
		}
		var delta map[string]any
		finish := "stop"
		if name != "" {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}
			finish = "tool_calls"
		} else {
			delta = map[string]any{"content": "Verified fixture values."}
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer server.Close()
	reg := tools.NewRegistry()
	executions := 0
	reg.MustRegister(tools.Tool{Name: "check_contract", Description: "Verify structured fixture arguments", Schema: discoveryWireSchema,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			executions++
			var decoded struct {
				Mode    string
				Count   int
				Options struct{ Enabled bool }
			}
			if err := json.Unmarshal(args, &decoded); err != nil {
				return tools.Result{Err: err}, nil
			}
			if decoded.Mode != "two  spaces" || decoded.Count != 2 || decoded.Options.Enabled {
				return tools.Result{Err: fmt.Errorf("fixture values changed")}, nil
			}
			return tools.Result{Text: "count=2 mode=two  spaces enabled=false"}, nil
		}})
	search := tools.NewToolSearcher(reg, nil).Spec()
	original := search.Fn
	public := ""
	search.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		result, err := original(ctx, args)
		public = result.Text
		if !projected {
			result.ModelText = ""
		}
		return result, err
	}
	reg.MustRegister(search)
	reg.MarkAlwaysOn("tool_search")
	if wrapped {
		reg.MustRegister(NewInvokeTool(reg).Spec())
		reg.MarkAlwaysOn("invoke_tool")
	}
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: server.URL, Model: "qwen-wire-fixture", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: wrapped, StableToolset: wrapped, MaxSteps: 5})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := loop.Run(ctx, "Discover check_contract, verify the fixture arguments, and report the result.")
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if failure, ok := event.(ErrorEvent); ok {
			t.Fatalf("loop failed: %v", failure.Err)
		}
	}
	measure := discoveryWireMeasure{Requests: int(requests.Load()), Executions: executions}
	for len(captured) > 0 {
		body := <-captured
		measure.Bytes += len(body)
		// Decode explicit wire tags rather than using llm.Message (whose JSON
		// is the internal persistence contract, not the provider transport).
		var request struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		for _, message := range request.Messages {
			if message.Role != "tool" {
				continue
			}
			if message.ToolCallID == "discover" {
				measure.DiscoveryBytes = len(message.Content)
				assertWireDiscoveryContract(t, public, message.Content, projected)
			}
			if message.ToolCallID == "check" && message.Content == "count=2 mode=two  spaces enabled=false" {
				measure.Quality = true
			}
		}
	}
	if measure.DiscoveryBytes == 0 {
		t.Fatal("discovery was absent from the serialized transport")
	}
	return measure
}

func assertWireDiscoveryContract(t *testing.T, public, model string, projected bool) {
	t.Helper()
	var before struct {
		Matches []struct{ Name, Schema string }
	}
	if err := json.Unmarshal([]byte(public), &before); err != nil {
		t.Fatal(err)
	}
	var after struct {
		Matches []struct {
			Name   string
			Schema json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(model), &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Matches) != 1 || len(after.Matches) != 1 || before.Matches[0].Name != "check_contract" || after.Matches[0].Name != "check_contract" {
		t.Fatal("discovery match changed")
	}
	actual := after.Matches[0].Schema
	if !projected {
		var text string
		if err := json.Unmarshal(actual, &text); err != nil {
			t.Fatal(err)
		}
		actual = []byte(text)
	} else if len(actual) == 0 || actual[0] != '{' {
		t.Fatal("model schema was still encoded as a JSON string")
	}
	decode := func(text string) any {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var result any
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !reflect.DeepEqual(decode(before.Matches[0].Schema), decode(string(actual))) {
		t.Fatal("serialized model schema lost contract fields")
	}
	if !strings.Contains(string(actual), `[9007199254740993,1e+09,-0]`) {
		t.Fatal("serialized schema changed numeric spellings")
	}
}
