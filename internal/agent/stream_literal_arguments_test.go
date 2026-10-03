package agent

import (
	"context"
	"encoding/json"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func literalToolArgsBlock(v string) string {
	return "<tool_call><function=fixture><parameter=value>" + v + "</parameter></function></tool_call>"
}
func TestLiteralToolArgsControls(t *testing.T) {
	valid, rejected := 0, 0
	for c := 0; c < 32; c++ {
		want := "prefix" + string(rune(c)) + "suffix"
		calls, _ := extractXMLToolCalls(literalToolArgsBlock(want))
		if len(calls) != 1 {
			t.Fatal("missing call")
		}
		tc := calls[0]
		if err := HardenToolCall(&tc, []string{"fixture"}, 0); err != "" {
			rejected++
			continue
		}
		var args map[string]string
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || args["value"] != want {
			t.Fatalf("control %d roundtrip: %v", c, err)
		}
		valid++
	}
	if valid != 32 || rejected != 0 {
		t.Fatalf("controls valid=%d rejected=%d", valid, rejected)
	}
	for _, v := range []string{"hello", `a\b "c"`, "Zażółć 日本語", "<div> & text </div>", "line1\\nline2", `{"nested":[1,"ż"]}`, `[1,2,"β"]`} {
		calls, _ := extractXMLToolCalls(literalToolArgsBlock(v))
		if len(calls) != 1 {
			t.Fatal("missing ordinary call")
		}
		tc := calls[0]
		if err := HardenToolCall(&tc, []string{"fixture"}, 0); err != "" {
			t.Fatal(err)
		}
		var args map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
			if string(args["value"]) != v {
				t.Fatal("raw structure changed")
			}
		} else {
			var got string
			if err := json.Unmarshal(args["value"], &got); err != nil || got != v {
				t.Fatalf("literal mismatch: %v", err)
			}
		}
	}
	bad, _ := extractXMLToolCalls(literalToolArgsBlock(`{"broken":}`))
	if len(bad) != 1 || HardenToolCall(&bad[0], []string{"fixture"}, 0) == "" {
		t.Fatal("invalid object reinterpreted as text")
	}
}

type literalToolArgsProvider struct {
	calls      int
	value, xml string
}

func (p *literalToolArgsProvider) Name() string { return "owned-text-json-fixture" }
func (p *literalToolArgsProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	lastTool := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleTool {
			lastTool = msgs[i].Content
			break
		}
	}
	ch := make(chan llm.Delta, 2)
	switch {
	case p.calls == 1:
		ch <- llm.Delta{Content: p.xml, FinishReason: "tool_calls"}
	case strings.HasPrefix(lastTool, badToolCallMarker):
		args, _ := json.Marshal(map[string]string{"value": p.value})
		call := llm.ToolCall{ID: "corrected-call", Name: "fixture", Arguments: string(args)}
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	default:
		ch <- llm.Delta{Content: "fixture complete", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}
func TestLiteralToolArgsLoopRepairTurn(t *testing.T) {
	v := "first line\nsecond\tline"
	p := &literalToolArgsProvider{value: v, xml: literalToolArgsBlock(v)}
	executed := 0
	captured := ""
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "fixture", Description: "owned read", ReadOnly: true, Schema: `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`, Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
		var args struct{ Value string }
		if err := json.Unmarshal(raw, &args); err != nil {
			return tools.Result{}, err
		}
		captured = args.Value
		executed++
		return tools.Result{Text: "owned fixture success"}, nil
	}, Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }})
	reg.Activate("fixture")
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: "synthetic fixture", MaxSteps: 4, SkipImplementationHint: true})
	if err != nil {
		t.Fatal(err)
	}
	events, err := loop.Run(context.Background(), "run fixture")
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for ev := range events {
		switch e := ev.(type) {
		case ToolResultEvent:
			if e.Err != nil {
				failures++
			}
		case ErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if executed != 1 || captured != v {
		t.Fatalf("execution lost: %d %q requests=%d failures=%d", executed, captured, p.calls, failures)
	}
	if p.calls != 2 || failures != 0 {
		t.Fatalf("spurious repair: calls=%d failures=%d", p.calls, failures)
	}
}
func TestLiteralToolArgsQuotedKeys(t *testing.T) {
	valid := 0
	for _, key := range []string{`key"quote`, `key\slash`, "[1,2]", "{}"} {
		calls, _ := extractXMLToolCalls("<tool_call><function=fixture><parameter=" + key + ">literal</parameter></function></tool_call>")
		if len(calls) != 1 {
			t.Fatal("missing key call")
		}
		var args map[string]string
		if json.Unmarshal([]byte(calls[0].Arguments), &args) == nil && args[key] == "literal" {
			valid++
		}
	}
	sv := 0
	for _, v := range []string{"tab\tinside", "cr\rinside", "esc\x1binside", "nul\x00inside"} {
		calls := parseSentinelBlock("fixture\nvalue: " + v)
		if len(calls) != 1 {
			t.Fatal("missing sentinel")
		}
		var args map[string]string
		if json.Unmarshal([]byte(calls[0].Arguments), &args) == nil && args["value"] == v {
			sv++
		}
	}
	if valid != 4 || sv != 4 {
		t.Fatalf("keys=%d controls=%d", valid, sv)
	}
}
func TestLiteralToolArgsUTF8AndLiteralKeys(t *testing.T) {
	for _, v := range []string{"<tag> & content", "line\u2028separator\u2029end", "a\xffb", "a\xc0\x80b"} {
		calls, _ := extractXMLToolCalls(literalToolArgsBlock(v))
		if len(calls) != 1 {
			t.Fatal("missing utf8 call")
		}
		var args map[string]string
		if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(v)
		var want string
		_ = json.Unmarshal(expected, &want)
		if args["value"] != want {
			t.Fatal("utf8 semantics changed")
		}
		if v == "<tag> & content" && strings.Contains(calls[0].Arguments, `\u003`) {
			t.Fatal("valid HTML needlessly expanded")
		}
	}
	valid := 0
	for _, key := range []string{`key"quote`, `key\slash`, "[1,2]", "{}"} {
		calls := parseSentinelBlock("fixture\n" + key + ": literal")
		if len(calls) != 1 {
			t.Fatal("missing sentinel key")
		}
		var args map[string]string
		if json.Unmarshal([]byte(calls[0].Arguments), &args) == nil && args[key] == "literal" {
			valid++
		}
	}
	if valid != 4 {
		t.Fatalf("literal sentinel keys=%d", valid)
	}
}
