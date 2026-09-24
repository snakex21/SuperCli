package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func toolStreamFragments(arguments string, width int) []string {
	var result []string
	for len(arguments) > 0 {
		n := min(width, len(arguments))
		for n < len(arguments) && !utf8.RuneStart(arguments[n]) {
			n--
		}
		result = append(result, arguments[:n])
		arguments = arguments[n:]
	}
	return result
}

func toolStreamArguments(repetitions int) string {
	raw, _ := json.Marshal(map[string]any{"path": "src/zażółć.go", "old": "old value", "new": strings.Repeat("new value: zażółć, 日本語, C:\\src\\file\n", repetitions)})
	return string(raw)
}

func anthropicToolStream(arguments string, width int) string {
	var body strings.Builder
	body.WriteString("data: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"patch-1\",\"name\":\"patch_file\",\"input\":{}}}\n\n")
	for _, fragment := range toolStreamFragments(arguments, width) {
		raw, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 3, "delta": map[string]string{"type": "input_json_delta", "partial_json": fragment}})
		fmt.Fprintf(&body, "data: %s\n\n", raw)
	}
	body.WriteString("data: {\"type\":\"content_block_stop\",\"index\":3}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	return body.String()
}

// Includes JSON/SSE parsing, not just the accumulator. The payload is prepared
// outside the timer; there is no model, network, filesystem or output consumer.
func BenchmarkAnthropicToolStream(b *testing.B) {
	for _, size := range []struct {
		name        string
		repetitions int
	}{{"small", 4}, {"large", 1400}} {
		arguments := toolStreamArguments(size.repetitions)
		body := anthropicToolStream(arguments, 32)
		b.Run(size.name, func(b *testing.B) {
			provider := &AnthropicProvider{}
			b.ReportAllocs()
			b.SetBytes(int64(len(arguments)))
			for b.Loop() {
				out := make(chan Delta, 4)
				if err := provider.streamSSE(context.Background(), strings.NewReader(body), out); err != nil {
					b.Fatal(err)
				}
				close(out)
				calls := 0
				for delta := range out {
					if delta.ToolCall != nil {
						calls++
						if delta.ToolCall.Arguments != arguments {
							b.Fatal("arguments changed")
						}
					}
				}
				if calls != 1 {
					b.Fatalf("calls=%d", calls)
				}
			}
		})
	}
}

func TestLargeToolArgumentsAcrossInterleavedOpenAIChunks(t *testing.T) {
	arguments := toolStreamArguments(1400)
	var chunks []string
	appendCall := func(index int, id, name, fragment string) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": id, "function": map[string]string{"name": name, "arguments": fragment}}}}}}})
		chunks = append(chunks, string(raw))
	}
	appendCall(7, "read-1", "read_many", "{")
	for i, fragment := range toolStreamFragments(arguments, 31) {
		id, name := "", ""
		if i == 0 {
			id, name = "patch-1", "patch_file"
		}
		appendCall(3, id, name, fragment)
		if i == 2 {
			appendCall(7, "", "", "\"reads\":\"src/a.go:1-2\"}")
		}
	}
	chunks = append(chunks, "{\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}", "{\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":22,\"total_tokens\":33}}")
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { sseResponse(w, chunks...) })
	provider, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Edit fixture"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls []ToolCall
	var usage *Usage
	for _, delta := range drainDeltas(t, stream) {
		if delta.Err != nil {
			t.Fatal(delta.Err)
		}
		if delta.ToolCall != nil {
			calls = append(calls, *delta.ToolCall)
		}
		if delta.Usage != nil {
			usage = delta.Usage
		}
	}
	if len(calls) != 2 || calls[0].ID != "patch-1" || calls[0].Name != "patch_file" || calls[0].Arguments != arguments || calls[1].ID != "read-1" || calls[1].Arguments != "{\"reads\":\"src/a.go:1-2\"}" {
		t.Fatalf("tool arguments lost or reordered; calls=%d", len(calls))
	}
	if usage == nil || usage.Input != 11 || usage.Output != 22 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestLargeToolArgumentsAcrossAnthropicChunks(t *testing.T) {
	arguments := toolStreamArguments(1400)
	out := make(chan Delta, 4)
	if err := (&AnthropicProvider{}).streamSSE(context.Background(), strings.NewReader(anthropicToolStream(arguments, 31)), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	calls := 0
	for delta := range out {
		if delta.ToolCall == nil {
			continue
		}
		calls++
		if delta.ToolCall.ID != "patch-1" || delta.ToolCall.Name != "patch_file" || delta.ToolCall.Arguments != arguments {
			t.Fatal("tool arguments changed")
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
