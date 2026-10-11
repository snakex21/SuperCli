package llm

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// This is the complete pre-guard implementation, intentionally unconditional.
// It is independent of the production admission check and remains the oracle
// for mutation, canonical hashing and signed-state pruning.
func protectAnthropicPrefixesUnconditionalForTest(req *anthropicRequest) {
	h := anthropicPrefixHash(req.System, req.Tools)
	for i := range req.Messages {
		m := &req.Messages[i]
		invalid := false
		for _, b := range m.Content {
			if b.Prefix != "" && b.Prefix != hex.EncodeToString(h.Sum(nil)) {
				invalid = true
				break
			}
		}
		if invalid {
			blocks := make([]anthropicContentBlock, 0, len(m.Content))
			for _, b := range m.Content {
				var kind struct{ Type string }
				_ = json.Unmarshal(b.Raw, &kind)
				if kind.Type != "thinking" && kind.Type != "redacted_thinking" {
					blocks = append(blocks, b)
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: "[no visible answer]"})
			}
			m.Content = blocks
		}
		writeAnthropicPrefix(h, *m)
	}
}

func TestAnthropicPrefixFastPathUnsignedWireParity(t *testing.T) {
	opaque := anthropicPrefixFastPathFixture(1)
	opaque.Messages = append(opaque.Messages, anthropicMessage{Role: "assistant", Content: []anthropicContentBlock{
		{Raw: json.RawMessage(`{"type":"thinking","thinking":"synthetic fixture","signature":"opaque+/=<&>","future":{"counter":9007199254740993,"counter":-0,"exp":1.2e3}}`)},
		{Raw: json.RawMessage(`{"type":"text","text":"visible synthetic evidence","future":{"keep":true}}`)},
	}})
	marked := anthropicPrefixFastPathFixture(32)
	marked.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	marked.Messages[0].Content[0].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	unicode := anthropicPrefixFastPathFixture(1)
	unicode.System = "Polski ąćęół; 日本語; 😀; <&>; \u2028\u2029"
	unicode.Messages[0].Content[0].Text = "quotes \\\"\n" + string([]byte{'x', 0xff, 0xfe, 'y'})
	for _, tc := range []struct {
		name string
		req  anthropicRequest
	}{
		{"nil slices", anthropicRequest{Model: "fixture", MaxTokens: 8192, Stream: true}},
		{"empty slices", anthropicRequest{Model: "fixture", MaxTokens: 8192, Stream: true, Messages: []anthropicMessage{}, Tools: []anthropicTool{}}},
		{"small", anthropicPrefixFastPathFixture(1)},
		{"coding 32", anthropicPrefixFastPathFixture(32)},
		{"coding 128", anthropicPrefixFastPathFixture(128)},
		{"opaque no prefix", opaque},
		{"cache markers", marked},
		{"unicode and replacement", unicode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, cacheSystem := range []bool{false, true} {
				// Production admits the system-block fallback only for a
				// nonempty stable system; preserve that helper's precondition.
				if cacheSystem && strings.TrimSpace(tc.req.System) == "" {
					continue
				}
				before, beforeErr := marshalAnthropicPromptCache(tc.req, cacheSystem)
				if beforeErr != nil {
					t.Fatal(beforeErr)
				}
				want := cloneAnthropicPrefixFastPathRequest(tc.req)
				got := cloneAnthropicPrefixFastPathRequest(tc.req)
				protectAnthropicPrefixesUnconditionalForTest(&want)
				protectAnthropicPrefixes(&got)
				wantBody, wantErr := marshalAnthropicPromptCache(want, cacheSystem)
				gotBody, gotErr := marshalAnthropicPromptCache(got, cacheSystem)
				if wantErr != nil || gotErr != nil || !bytes.Equal(before, wantBody) || !bytes.Equal(wantBody, gotBody) || !reflect.DeepEqual(want, got) {
					t.Fatalf("unsigned request changed: %v / %v", wantErr, gotErr)
				}
				if anthropicRequestPrefix(gotBody) == "" || anthropicRequestPrefix(gotBody) != anthropicRequestPrefix(wantBody) {
					t.Fatal("accepted-request prefix changed")
				}
				after, err := marshalAnthropicPromptCache(tc.req, cacheSystem)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("caller fixture mutated")
				}
			}
		})
	}
}

func TestAnthropicPrefixFastPathSignedAndMismatchParity(t *testing.T) {
	for _, change := range []string{"matching", "system changed", "tools changed", "history changed", "marker changed", "late metadata", "late mismatch", "thinking only mismatch", "ordinary metadata"} {
		t.Run(change, func(t *testing.T) {
			req := anthropicPrefixFastPathSignedFixture(8)
			last := &req.Messages[len(req.Messages)-1]
			switch change {
			case "system changed":
				req.System += "\nA new current constraint."
			case "tools changed":
				req.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"new":{"type":"string"}}}`)
			case "history changed":
				req.Messages[0].Content[0].Text += "\nCorrection to the task."
			case "marker changed":
				req.Messages[0].Content[0].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
			case "late metadata", "late mismatch":
				for i := 0; i < len(last.Content)-1; i++ {
					last.Content[i].Prefix = ""
				}
				if change == "late mismatch" {
					last.Content[len(last.Content)-1].Prefix = "different prefix"
				}
			case "thinking only mismatch":
				last.Content = last.Content[:2]
				for i := range last.Content {
					last.Content[i].Prefix = "different prefix"
				}
			case "ordinary metadata":
				last.Content = []anthropicContentBlock{{Type: "text", Text: "visible", Prefix: "different prefix"}}
			}
			before, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			want, got := cloneAnthropicPrefixFastPathRequest(req), cloneAnthropicPrefixFastPathRequest(req)
			protectAnthropicPrefixesUnconditionalForTest(&want)
			protectAnthropicPrefixes(&got)
			wantBody, wantErr := json.Marshal(want)
			gotBody, gotErr := json.Marshal(got)
			if wantErr != nil || gotErr != nil || !bytes.Equal(wantBody, gotBody) || !reflect.DeepEqual(want, got) {
				t.Fatalf("signed mutation/wire differs: %v / %v", wantErr, gotErr)
			}
			tail := got.Messages[len(got.Messages)-1].Content
			switch change {
			case "matching", "marker changed", "late metadata":
				if len(tail) != 4 || !bytes.Equal(before, gotBody) {
					t.Fatal("valid native blocks or raw numeric spelling changed")
				}
			case "system changed", "tools changed", "history changed", "late mismatch":
				if len(tail) != 2 || !strings.Contains(string(gotBody), "visible synthetic evidence") || !strings.Contains(string(gotBody), "tool_use") || strings.Contains(string(gotBody), `"signature"`) {
					t.Fatal("mismatch pruning lost evidence/tool blocks or retained signed thinking")
				}
			case "thinking only mismatch":
				if len(tail) != 1 || tail[0].Text != "[no visible answer]" {
					t.Fatal("empty signed answer fallback changed")
				}
			case "ordinary metadata":
				if len(tail) != 1 || tail[0].Text != "visible" {
					t.Fatal("metadata on ordinary block changed old behavior")
				}
			}
			after, err := json.Marshal(req)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("source archive mutated")
			}
		})
	}
}

func TestAnthropicPrefixFastPathPreservesRequestMarshalErrors(t *testing.T) {
	nan := math.NaN()
	for _, tc := range []struct {
		name        string
		block       anthropicContentBlock
		tools       []anthropicTool
		temperature *float64
	}{
		{"nil raw typed block", anthropicContentBlock{Type: "text", Text: "valid"}, nil, nil},
		{"empty nonnil raw", anthropicContentBlock{Raw: json.RawMessage{}}, nil, nil},
		{"truncated opaque raw", anthropicContentBlock{Raw: json.RawMessage(`{"type":"thinking","signature":`)}, nil, nil},
		{"invalid native numeric JSON", anthropicContentBlock{Raw: json.RawMessage(`{"future":NaN}`)}, nil, nil},
		{"invalid tool schema raw", anthropicContentBlock{Type: "text", Text: "valid"}, []anthropicTool{{Name: "bad", InputSchema: json.RawMessage(`{"broken":`)}}, nil},
		{"unsupported typed input", anthropicContentBlock{Type: "tool_use", Input: map[string]any{"bad": func() {}}}, nil, nil},
		{"nonfinite sampling", anthropicContentBlock{Type: "text", Text: "valid"}, nil, &nan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := anthropicRequest{Model: "fixture", MaxTokens: 8192, Stream: true, Messages: []anthropicMessage{{Role: "assistant", Content: []anthropicContentBlock{tc.block}}}, Tools: tc.tools, Temperature: tc.temperature}
			want, got := cloneAnthropicPrefixFastPathRequest(req), cloneAnthropicPrefixFastPathRequest(req)
			protectAnthropicPrefixesUnconditionalForTest(&want)
			protectAnthropicPrefixes(&got)
			for _, cacheSystem := range []bool{false, true} {
				wantBody, wantErr := marshalAnthropicPromptCache(want, cacheSystem)
				gotBody, gotErr := marshalAnthropicPromptCache(got, cacheSystem)
				if !bytes.Equal(wantBody, gotBody) || fmt.Sprintf("%T:%v", wantErr, wantErr) != fmt.Sprintf("%T:%v", gotErr, gotErr) {
					t.Fatalf("public encoder error changed: %v / %v", wantErr, gotErr)
				}
				var wantMarshaler, gotMarshaler *json.MarshalerError
				wantHas, gotHas := errors.As(wantErr, &wantMarshaler), errors.As(gotErr, &gotMarshaler)
				if wantHas != gotHas || (wantHas && (wantMarshaler.Type != gotMarshaler.Type || fmt.Sprintf("%T:%v", wantMarshaler.Err, wantMarshaler.Err) != fmt.Sprintf("%T:%v", gotMarshaler.Err, gotMarshaler.Err))) {
					t.Fatal("opaque MarshalerError classification changed")
				}
			}
		})
	}
}

type anthropicPrefixMarshalProbe struct{ calls *atomic.Int64 }

func (p anthropicPrefixMarshalProbe) MarshalJSON() ([]byte, error) {
	p.calls.Add(1)
	return []byte(`"synthetic probe"`), nil
}

func TestAnthropicPrefixFastPathSkipsUnusedCanonicalMarshal(t *testing.T) {
	var calls atomic.Int64
	req := anthropicPrefixFastPathFixture(1)
	req.Messages[0].Content = []anthropicContentBlock{{Type: "tool_use", ID: "fixture", Name: "fixture", Input: map[string]any{"probe": anthropicPrefixMarshalProbe{calls: &calls}}}}
	protectAnthropicPrefixesUnconditionalForTest(&req)
	if calls.Load() != 1 {
		t.Fatal("old oracle did not perform the redundant canonical marshal")
	}
	calls.Store(0)
	protectAnthropicPrefixes(&req)
	if calls.Load() != 0 {
		t.Fatal("unsigned request still performed canonical hashing")
	}
	// Any nonempty metadata must retain the original validation path, even
	// when it is on the last ordinary block rather than a thinking block.
	req.Messages[0].Content = append(req.Messages[0].Content, anthropicContentBlock{Type: "text", Text: "visible", Prefix: "saved metadata"})
	protectAnthropicPrefixes(&req)
	if calls.Load() != 1 {
		t.Fatal("late saved prefix bypassed the protection path")
	}
}

func BenchmarkAnthropicPrefixProtectionFastPath(b *testing.B) {
	for _, messages := range []int{1, 32, 128} {
		for _, signed := range []bool{false, true} {
			name := fmt.Sprintf("messages_%d/unsigned", messages)
			req := anthropicPrefixFastPathFixture(messages)
			if signed {
				name = fmt.Sprintf("messages_%d/signed_control", messages)
				req = anthropicPrefixFastPathSignedFixture(messages)
			}
			body, err := json.Marshal(req)
			if err != nil {
				b.Fatal(err)
			}
			for _, path := range []struct {
				name string
				fn   func(*anthropicRequest)
			}{{"unconditional", protectAnthropicPrefixesUnconditionalForTest}, {"guarded", protectAnthropicPrefixes}} {
				b.Run(name+"/"+path.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						path.fn(&req)
					}
					b.StopTimer()
					b.ReportMetric(float64(len(body)), "wire-B")
				})
			}
		}
	}
}

func anthropicPrefixFastPathFixture(messages int) anthropicRequest {
	req := anthropicRequest{Model: "prefix-fixture", MaxTokens: 8192, Stream: true, System: "Keep the selected reasoning, tests, paths and current user agreements."}
	if messages == 1 {
		req.Messages = []anthropicMessage{{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: "hello"}}}}
		return req
	}
	req.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: 6144}
	req.Tools = []anthropicTool{{Name: "search_code", Description: "Search synthetic source", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","maximum":32,"default":3}}}`)}}
	text := strings.Repeat("synthetic source diff; path C:\\repo\\src.go; ąćęół <&>\n", 48)
	for i := range messages {
		id := fmt.Sprintf("call_%d", i/4)
		var message anthropicMessage
		switch i % 4 {
		case 0:
			message = anthropicMessage{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: "Inspect the code and preserve current constraints. " + text}}}
		case 1:
			message = anthropicMessage{Role: "assistant", Content: []anthropicContentBlock{{Type: "tool_use", ID: id, Name: "search_code", Input: map[string]any{"query": "context", "limit": 3}}}}
		case 2:
			message = anthropicMessage{Role: "user", Content: []anthropicContentBlock{{Type: "tool_result", ToolUseID: id, Content: text}}}
		case 3:
			message = anthropicMessage{Role: "assistant", Content: []anthropicContentBlock{{Type: "text", Text: "Observed code, completed check, continuing task. " + text}}}
		}
		req.Messages = append(req.Messages, message)
	}
	return req
}

func anthropicPrefixFastPathSignedFixture(messages int) anthropicRequest {
	req := anthropicPrefixFastPathFixture(messages)
	h := anthropicPrefixHash(req.System, req.Tools)
	for _, message := range req.Messages {
		writeAnthropicPrefix(h, message)
	}
	prefix := hex.EncodeToString(h.Sum(nil))
	req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: []anthropicContentBlock{
		{Prefix: prefix, Raw: json.RawMessage(`{"type":"thinking","thinking":"synthetic fixture","signature":"opaque+/=<&>","future":{"counter":9007199254740993,"zero":-0,"exp":1.2e3}}`)},
		{Prefix: prefix, Raw: json.RawMessage(`{"type":"redacted_thinking","data":"opaque redacted state","future":true}`)},
		{Prefix: prefix, Raw: json.RawMessage(`{"type":"text","text":"visible synthetic evidence","future":{"keep":true}}`)},
		{Prefix: prefix, Raw: json.RawMessage(`{"type":"tool_use","id":"native_call","name":"search_code","input":{"query":"context"},"future":9007199254740993}`)},
	}})
	return req
}

func cloneAnthropicPrefixFastPathRequest(req anthropicRequest) anthropicRequest {
	if req.Messages != nil {
		req.Messages = append([]anthropicMessage{}, req.Messages...)
		for i := range req.Messages {
			if req.Messages[i].Content != nil {
				req.Messages[i].Content = append([]anthropicContentBlock{}, req.Messages[i].Content...)
				for j := range req.Messages[i].Content {
					if raw := req.Messages[i].Content[j].Raw; raw != nil {
						req.Messages[i].Content[j].Raw = append(json.RawMessage{}, raw...)
					}
				}
			}
		}
	}
	if req.Tools != nil {
		req.Tools = append([]anthropicTool{}, req.Tools...)
		for i := range req.Tools {
			if raw := req.Tools[i].InputSchema; raw != nil {
				req.Tools[i].InputSchema = append(json.RawMessage{}, raw...)
			}
		}
	}
	return req
}
