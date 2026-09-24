package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStreamedToolSnapshotStaysImmutable(t *testing.T) {
	call := &streamedToolCall{ID: "one", Name: "read_many"}
	call.arguments.WriteString("{\"reads\":\"a.go\"}")
	first := call.snapshot()
	call.arguments.Reset()
	call.ID, call.Name = "two", "search_code"
	call.arguments.WriteString(strings.Repeat("new bytes", 1000))
	if first.ID != "one" || first.Name != "read_many" || first.Arguments != "{\"reads\":\"a.go\"}" {
		t.Fatal("a completed call changed")
	}
}

func TestAnthropicToolArgumentSeedsAndInterleaving(t *testing.T) {
	tests := []struct {
		name, input string
		fragments   []string
		want        string
	}{
		{"empty input", "{}", nil, "{}"},
		{"missing input", "", nil, "{}"},
		{"complete input", "{\"path\":\"a.go\"}", nil, "{\"path\":\"a.go\"}"},
		{"empty deltas before input", "{}", []string{"", "", "{\"path\":", "\"a.go\"}"}, "{\"path\":\"a.go\"}"},
		{"empty arguments remain empty", "{}", []string{""}, ""},
		{"partial remains partial", "{}", []string{"{\"path\":\"a.go"}, "{\"path\":\"a.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stream strings.Builder
			write := func(event map[string]any) {
				raw, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&stream, "data: %s\n\n", raw)
			}
			block := map[string]any{"type": "tool_use", "id": "first", "name": "read_lines"}
			if tt.input != "" {
				block["input"] = json.RawMessage(tt.input)
			}
			write(map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
			write(map[string]any{"type": "content_block_start", "index": 5, "content_block": map[string]any{"type": "tool_use", "id": "second", "name": "list_dir", "input": map[string]string{"path": "."}}})
			for _, fragment := range tt.fragments {
				write(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": fragment}})
			}
			write(map[string]any{"type": "content_block_stop", "index": 5})
			write(map[string]any{"type": "content_block_stop", "index": 0})
			write(map[string]any{"type": "message_stop"})
			out := make(chan Delta, 4)
			if err := (&AnthropicProvider{}).streamSSE(context.Background(), strings.NewReader(stream.String()), out); err != nil {
				t.Fatal(err)
			}
			close(out)
			var calls []ToolCall
			for delta := range out {
				if delta.ToolCall != nil {
					calls = append(calls, *delta.ToolCall)
				}
			}
			want := []ToolCall{{ID: "second", Name: "list_dir", Arguments: "{\"path\":\".\"}"}, {ID: "first", Name: "read_lines", Arguments: tt.want}}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("got %+v want %+v", calls, want)
			}
		})
	}
}
