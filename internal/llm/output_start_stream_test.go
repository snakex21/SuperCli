package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBufferedToolOutputStartsBeforeExecutableCall(t *testing.T) {
	for _, tc := range []struct {
		name           string
		prefix, suffix []string
		makeProvider   func(string) (Provider, error)
	}{
		{"chat", []string{
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_lines","arguments":"{\"path\":"}}]}}]}`,
		}, []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12000,"completion_tokens":7}}`,
		}, func(base string) (Provider, error) { return NewOpenAI(OpenAIConfig{BaseURL: base, Model: "m"}) }},
		{"responses", []string{
			`{"type":"response.created","response":{}}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"read_lines","arguments":""}}`,
		}, []string{
			`{"type":"response.function_call_arguments.delta","delta":"{\"path\":\"a.go\"}"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"read_lines","arguments":"{\"path\":\"a.go\"}"}}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":12000,"output_tokens":7}}}`,
		}, func(base string) (Provider, error) { return NewResponses(ResponsesConfig{BaseURL: base, Model: "m"}) }},
		{"anthropic", []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":12000}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"read_lines","input":{}}}`,
		}, []string{
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		}, func(base string) (Provider, error) {
			return NewAnthropic(AnthropicConfig{BaseURL: base, Model: "m", APIKey: "test"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				send := func(frames []string) {
					for _, frame := range frames {
						_, _ = io.WriteString(w, "data: "+frame+"\n\n")
					}
					w.(http.Flusher).Flush()
				}
				send(tc.prefix[:1])
				time.Sleep(20 * time.Millisecond)
				send(tc.prefix[1:])
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				send(tc.suffix)
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			p, err := tc.makeProvider(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			capture := &sinkCapture{}
			ch, err := Metered(p, "test", PurposeMain, capture.sink()).Complete(ctx, []Message{{Role: RoleUser, Content: "read file"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			markers := 0
			var calls []ToolCall
			var usage *Usage
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
				if d.OutputStarted {
					markers++
					if markers == 1 {
						close(release)
					}
					if d.ToolCall != nil || d.Content != "" || d.Reasoning != "" {
						t.Fatal("progress carries partial output")
					}
				}
				if d.ToolCall != nil {
					if markers == 0 {
						t.Fatal("executable call arrived before output-start signal")
					}
					calls = append(calls, *d.ToolCall)
				}
				if d.Usage != nil {
					usage = d.Usage
				}
			}
			if markers != 1 || len(calls) != 1 {
				t.Fatalf("markers=%d calls=%v", markers, calls)
			}
			if calls[0].ID != "call_1" || calls[0].Name != "read_lines" || calls[0].Arguments != `{"path":"a.go"}` {
				t.Fatalf("call changed: %+v", calls[0])
			}
			if usage == nil || usage.Input != 12000 || usage.Output != 7 {
				t.Fatalf("usage changed: %+v", usage)
			}
			if s := capture.all()[0]; s.TTFT < 15*time.Millisecond || s.Failed || s.Canceled {
				t.Fatalf("unexpected failure: %+v", s)
			}
		})
	}
}

func TestDeltaHasModelOutput(t *testing.T) {
	for _, d := range []Delta{
		{Content: " "}, {Reasoning: "analysis"}, {ToolCall: &ToolCall{Name: "read_lines"}},
		{NativeReasoning: &ReasoningBlock{}}, {OutputStarted: true},
	} {
		if !d.HasModelOutput() {
			t.Errorf("missed output: %+v", d)
		}
	}
	for _, d := range []Delta{
		{}, {Role: RoleAssistant}, {Usage: &Usage{Input: 1}}, {FinishReason: "stop"},
		{Notice: "queued"}, {Err: errSentinel}, {Content: "ignored", Notice: "queued"},
	} {
		if d.HasModelOutput() {
			t.Errorf("metadata treated as output: %+v", d)
		}
	}
}
