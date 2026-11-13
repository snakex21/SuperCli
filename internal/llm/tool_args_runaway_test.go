package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Live regression (gpt-5.4-nano, Responses API, "count active rows" task): a
// ctx_execute call degenerated into an endless " \n \n ..." argument stream,
// ~70k output tokens over seven minutes, until the harness killed the process.
// Both streaming transports must stop such a call after a bounded amount.
func TestRunawayToolArgumentsStopTheStream(t *testing.T) {
	for _, transport := range []string{"chat", "responses"} {
		t.Run(transport, func(t *testing.T) {
			var sent atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				fl := w.(http.Flusher)
				write := func(s string) bool {
					if _, err := fmt.Fprintf(w, "data: %s\n\n", s); err != nil {
						return false
					}
					fl.Flush()
					return r.Context().Err() == nil
				}
				if transport == "chat" {
					write(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ctx_execute","arguments":"{\"command\":[\"cmd\",\"/c\",\"x"}}]}}]}`)
				} else {
					write(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"ctx_execute","arguments":""}}`)
				}
				// "Endless" degenerate output: bounded only so a broken guard fails
				// the assertion below instead of hanging the test.
				for i := 0; i < 200000; i++ {
					var chunk string
					if transport == "chat" {
						chunk = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \\n"}}]}}]}`
					} else {
						chunk = `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":" \\n"}`
					}
					if !write(chunk) {
						return
					}
					sent.Add(1)
				}
			}))
			defer srv.Close()
			var p Provider
			var err error
			if transport == "chat" {
				p, err = NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "fixture"})
			} else {
				p, err = NewResponses(ResponsesConfig{BaseURL: srv.URL, Model: "fixture"})
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "count rows"}}, []ToolDef{{Name: "ctx_execute", Description: "run", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}}}`}})
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			var calls int
			for d := range ch {
				if d.Err != nil && streamErr == nil {
					streamErr = d.Err
				}
				if d.ToolCall != nil {
					calls++
				}
			}
			if streamErr == nil || !strings.Contains(streamErr.Error(), "runaway tool call") {
				t.Fatalf("degenerate tool call was not stopped: err=%v calls=%d", streamErr, calls)
			}
			if calls != 0 {
				t.Fatalf("a runaway call must not be executed: %d calls", calls)
			}
			if n := sent.Load(); n > 20000 {
				t.Fatalf("server streamed %d chunks before the guard fired", n)
			}
		})
	}
}

func TestRunawayGuardAllowsRealArguments(t *testing.T) {
	big := strings.Repeat(`line of real code with \"quotes\" and\ttabs\n`, 2000)
	var b strings.Builder
	for i := 0; i < len(big); i += 7 {
		end := i + 7
		if end > len(big) {
			end = len(big)
		}
		prev := b.Len()
		b.WriteString(big[i:end])
		if err := checkRunawayToolArguments("create_file", &b, prev); err != nil {
			t.Fatalf("legitimate content flagged at %d bytes: %v", b.Len(), err)
		}
	}
	// A long indentation run below the window is fine too.
	b.Reset()
	b.WriteString(strings.Repeat(" ", runawayToolArgsWindow-1) + "x")
	if err := checkRunawayToolArguments("create_file", &b, 0); err != nil {
		t.Fatal(err)
	}
}
