package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 1.0.38: while the model streams a large tool call (a whole file's content),
// providers report the streamed size so the GUI can say "model is writing
// create_file (7 KB)" instead of "model thinking" with no visible progress.

func TestToolArgsProgressThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	saved := toolStreamClock
	toolStreamClock = func() time.Time { return now }
	t.Cleanup(func() { toolStreamClock = saved })
	var p toolArgsProgress
	if got := p.next("create_file", toolArgsProgressMin-1); got != nil {
		t.Fatalf("small call reported: %+v", got)
	}
	base := toolArgsProgressMin
	if got := p.next("create_file", base); got == nil || got.Bytes != base || got.Name != "create_file" {
		t.Fatalf("first report = %+v", got)
	}
	if got := p.next("create_file", base+900); got != nil {
		t.Fatalf("throttled report leaked: %+v", got)
	}
	if got := p.next("create_file", base+toolArgsProgressStep); got == nil {
		t.Fatal("size step not reported")
	}
	now = now.Add(toolArgsProgressEvery)
	if got := p.next("create_file", base+toolArgsProgressStep+5); got == nil {
		t.Fatal("time step not reported")
	}
	if got := p.next("patch_file", 3); got != nil {
		t.Fatalf("small next call reported: %+v", got)
	}
	if got := p.next("patch_file", base); got == nil || got.Name != "patch_file" {
		t.Fatalf("next call not reported: %+v", got)
	}
}

func TestProvidersReportStreamedToolArgs(t *testing.T) {
	content := strings.Repeat("const x = 1;\n", 600) // ~7.8 KB
	argsJSON, _ := json.Marshal(map[string]string{"path": "a.zig", "content": content})
	args := string(argsJSON)
	chunks := []string{}
	for i := 0; i < len(args); i += 500 {
		chunks = append(chunks, args[i:min(i+500, len(args))])
	}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var chat, responses []string
	chat = append(chat, `{"choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"create_file","arguments":""}}]}}]}`)
	responses = append(responses, `{"type":"response.created","response":{}}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"create_file","arguments":""}}`)
	for _, c := range chunks {
		chat = append(chat, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":`+q(c)+`}}]}}]}`)
		responses = append(responses, `{"type":"response.function_call_arguments.delta","delta":`+q(c)+`}`)
	}
	chat = append(chat, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":7}}`)
	responses = append(responses,
		`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"create_file","arguments":`+q(args)+`}}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":7}}}`)

	for _, tc := range []struct {
		name   string
		frames []string
		make   func(string) (Provider, error)
	}{
		{"chat", chat, func(base string) (Provider, error) { return NewOpenAI(OpenAIConfig{BaseURL: base, Model: "m"}) }},
		{"responses", responses, func(base string) (Provider, error) { return NewResponses(ResponsesConfig{BaseURL: base, Model: "m"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, frame := range tc.frames {
					_, _ = io.WriteString(w, "data: "+frame+"\n\n")
				}
				w.(http.Flusher).Flush()
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p, err := tc.make(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "write"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var reports []ToolArgsProgress
			var calls []ToolCall
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
				if d.ToolArgs != nil {
					if d.ToolCall != nil || d.Content != "" || d.HasModelOutput() {
						t.Fatalf("progress delta carries output: %+v", d)
					}
					reports = append(reports, *d.ToolArgs)
				}
				if d.ToolCall != nil {
					calls = append(calls, *d.ToolCall)
				}
			}
			if len(calls) != 1 || calls[0].Arguments != args {
				t.Fatalf("call changed: %d calls", len(calls))
			}
			if len(reports) < 2 || len(reports) > len(chunks) {
				t.Fatalf("reports = %d for %d chunks (want throttled, at least start + one step)", len(reports), len(chunks))
			}
			for i, r := range reports {
				if r.Name != "create_file" || (i > 0 && r.Bytes <= reports[i-1].Bytes) {
					t.Fatalf("report %d = %+v", i, r)
				}
			}
		})
	}
}
