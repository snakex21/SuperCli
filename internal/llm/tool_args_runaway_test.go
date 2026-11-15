package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Live regression (gpt-5.4-nano, Responses API, "count active rows" task): a
// ctx_execute call degenerated into an endless " \n \n ..." argument stream,
// ~70k output tokens over seven minutes, until the harness killed the process.
// Both streaming transports must stop such a call after a bounded amount.
//
// The bound is checked on what the client consumed, not on how much the
// server managed to write: the server streams at most runawayTestChunks
// degenerate chunks and then holds the stream open without data. A guard
// that fires within those chunks ends the stream with the runaway error and
// hangs up; a missing or late guard leaves the client waiting until the
// server gives up and ends the stream normally, which fails the test. The
// old check ("the server sent at most 20000 chunks") also counted what the
// kernel buffered after the client had stopped reading, so it failed
// intermittently under load.
const runawayTestChunks = 20000

func TestRunawayToolArgumentsStopTheStream(t *testing.T) {
	for _, transport := range []string{"chat", "responses"} {
		t.Run(transport, func(t *testing.T) {
			disconnected := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Report a client hang-up however the handler ends.
				defer func() {
					select {
					case <-r.Context().Done():
						close(disconnected)
					case <-time.After(10 * time.Second):
					}
				}()
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
				for i := 0; i < runawayTestChunks; i++ {
					var chunk string
					if transport == "chat" {
						chunk = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \\n"}}]}}]}`
					} else {
						chunk = `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":" \\n"}`
					}
					if !write(chunk) {
						return
					}
				}
				// Every degenerate chunk is out: hold the stream open without data
				// until the client hangs up. A client that has not stopped the
				// call is still waiting; end its stream normally after a while so
				// the test fails on the missing runaway error instead of hanging.
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
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
				t.Fatalf("degenerate tool call was not stopped within %d chunks: err=%v calls=%d", runawayTestChunks, streamErr, calls)
			}
			if calls != 0 {
				t.Fatalf("a runaway call must not be executed: %d calls", calls)
			}
			// Stopping the call also means hanging up, not draining the stream.
			select {
			case <-disconnected:
			case <-time.After(10 * time.Second):
				t.Fatal("client kept the runaway stream open")
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
