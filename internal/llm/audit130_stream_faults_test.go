package llm

// Audit-130: provider stream faults against the real OpenAI Chat Completions
// and Responses transports (both wires), using a 127.0.0.1 mock only.
// Every scenario asserts termination, the error surface and the timing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func a130TextStream(wire a130Wire, w http.ResponseWriter, parts ...string) {
	a130SSEHeaders(w)
	for _, p := range parts {
		if wire == wireChat {
			a130Write(w, chatContentLine(p))
		} else {
			a130Write(w, respTextLine(p))
		}
	}
}

func a130Finish(wire a130Wire, w http.ResponseWriter) {
	if wire == wireChat {
		a130Write(w, chatFinishLine("stop")+chatDoneLine)
	} else {
		a130Write(w, respCompletedLine(nil))
	}
}

func a130ToolStart(wire a130Wire, w http.ResponseWriter, firstArgs string) {
	a130SSEHeaders(w)
	if wire == wireChat {
		a130Write(w, chatToolLine("call_1", "write_file", firstArgs))
	} else {
		a130Write(w, respToolAddedLine("write_file"))
		a130Write(w, respArgsDeltaLine(firstArgs))
	}
}

func a130ToolArgs(wire a130Wire, w http.ResponseWriter, args string) bool {
	if wire == wireChat {
		return a130Write(w, chatToolLine("", "", args))
	}
	return a130Write(w, respArgsDeltaLine(args))
}

// 1a. Upstream crashes after partial answer text.
func TestA130_MidStreamAbortAfterPartialText(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 5, false)
			check := a130GoroutineBaseline(t)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, "partial ", "answer")
				time.Sleep(20 * time.Millisecond)
				a130Abort()
			})
			p, tr := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			t.Logf("%s: err=%v content=%q elapsed=%s requests=%d", wire, res.Err, res.Content, res.Elapsed, m.requests.Load())
			if res.Err == nil {
				t.Fatalf("aborted stream reported success: %+v", res)
			}
			if res.Content != "partial answer" {
				t.Fatalf("partial text = %q", res.Content)
			}
			if len(res.Calls) != 0 || res.Finish != "" {
				t.Fatalf("aborted stream produced calls/finish: %+v", res)
			}
			if res.Elapsed > 3*time.Second {
				t.Fatalf("abort took %s", res.Elapsed)
			}
			if got := m.requests.Load(); got != 1 {
				t.Fatalf("provider replayed an accepted stream: %d requests", got)
			}
			m.srv.Close()
			check(2, tr.CloseIdleConnections)
		})
	}
}

// 1b. Upstream crashes inside a partial tool-call argument: the partial call
// must never be emitted as executable.
func TestA130_MidStreamAbortInsideToolArguments(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, `{"path":"main.go","content":"package ma`)
				time.Sleep(20 * time.Millisecond)
				a130Abort()
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			t.Logf("%s: err=%v calls=%d elapsed=%s", wire, res.Err, len(res.Calls), res.Elapsed)
			if res.Err == nil || len(res.Calls) != 0 {
				t.Fatalf("partial tool call leaked: err=%v calls=%+v", res.Err, res.Calls)
			}
			if !strings.Contains(strings.ToLower(res.Err.Error()), "unexpected eof") {
				t.Logf("note: abort error text is %q", res.Err)
			}
		})
	}
}

// 1c. Gateway closes the body CLEANLY (no [DONE], no finish_reason, no
// response.completed) inside tool-call arguments. Chat Completions accepts a
// clean EOF as a normal end ("gateways omit [DONE]") and flushes the
// accumulated call — with truncated, invalid JSON — under finish_reason
// "tool_calls". The Responses wire reports an error instead.
func TestA130_CleanEOFInsideToolArguments(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, `{"path":"main.go","content":"package ma`)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			for _, c := range res.Calls {
				t.Logf("%s: emitted call %s args=%q validJSON=%v finish=%q", wire, c.Name, c.Arguments, json.Valid([]byte(c.Arguments)), res.Finish)
			}
			t.Logf("%s: err=%v finish=%q", wire, res.Err, res.Finish)
			if wire == wireResponses {
				if res.Err == nil || len(res.Calls) != 0 {
					t.Fatalf("responses: want error and no call, got %+v", res)
				}
				return
			}
			// Chat: pin the observed behaviour; the agent's argument validation
			// is the only thing that keeps this call from running.
			if len(res.Calls) == 1 && !json.Valid([]byte(res.Calls[0].Arguments)) && res.Finish == "tool_calls" && res.Err == nil {
				t.Logf("WEAKNESS: truncated tool call emitted as complete (finish=tool_calls)")
				return
			}
			if res.Err == nil && len(res.Calls) > 0 {
				t.Fatalf("unexpected chat behaviour: %+v", res)
			}
		})
	}
}

// 1d. Malformed SSE: one bad JSON line after valid output.
func TestA130_MalformedJSONLine(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, "ok ")
				a130Write(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}\n\n")
				a130Finish(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			t.Logf("%s: err=%v content=%q elapsed=%s", wire, res.Err, res.Content, res.Elapsed)
			if res.Err == nil {
				t.Fatalf("malformed JSON line accepted silently: %+v", res)
			}
			if res.Elapsed > 2*time.Second || m.requests.Load() != 1 {
				t.Fatalf("elapsed=%s requests=%d", res.Elapsed, m.requests.Load())
			}
		})
	}
}

// 1e. Missing terminal marker: chat sends finish_reason but no [DONE]
// (accepted); responses sends text but never response.completed (error).
func TestA130_MissingTerminalMarker(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, "hello")
				if wire == wireChat {
					a130Write(w, chatFinishLine("stop")) // no [DONE]
				}
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			t.Logf("%s: err=%v finish=%q content=%q", wire, res.Err, res.Finish, res.Content)
			if wire == wireChat && (res.Err != nil || res.Finish != "stop" || res.Content != "hello") {
				t.Fatalf("chat without [DONE]: %+v", res)
			}
			if wire == wireResponses && (res.Err == nil || !strings.Contains(res.Err.Error(), "stream ended before response.completed")) {
				t.Fatalf("responses without completed: %+v", res)
			}
		})
	}
}

// 1f. CRLF line endings, comments, keepalive data frames and unknown event
// types must all be tolerated.
func TestA130_CRLFCommentsKeepalivesUnknownEvents(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130SSEHeaders(w)
				a130Write(w, ": keepalive comment\r\n\r\n")
				a130Write(w, "data: ping\r\n\r\n")
				a130Write(w, "event: unknown_vendor_event\r\nid: 7\r\nretry: 1000\r\ndata: {\"type\":\"vendor.unknown\",\"x\":1}\r\n\r\n")
				if wire == wireChat {
					a130Write(w, crlf(chatContentLine("Hel")))
					a130Write(w, ": still alive\r\n")
					a130Write(w, crlf(chatContentLine("lo")))
					a130Write(w, crlf(chatFinishLine("stop")+chatDoneLine))
				} else {
					a130Write(w, crlf(respLine(map[string]any{"type": "response.created", "response": map[string]any{"id": "r1"}})))
					a130Write(w, crlf(respLine(map[string]any{"type": "response.in_progress"})))
					a130Write(w, crlf(respTextLine("Hel")))
					a130Write(w, ": still alive\r\n")
					a130Write(w, crlf(respLine(map[string]any{"type": "response.content_part.added"})))
					a130Write(w, crlf(respTextLine("lo")))
					a130Write(w, crlf(respCompletedLine(nil)))
				}
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			if res.Err != nil || res.Content != "Hello" || res.Finish != "stop" {
				t.Fatalf("%s: tolerant parse failed: err=%v content=%q finish=%q", wire, res.Err, res.Content, res.Finish)
			}
		})
	}
}

// 1g. Huge tool arguments streamed in 8 KiB deltas (5 MB of a repeating
// 8-byte unit). Since 1.0.30 the degenerate-arguments guard stops it at the
// 16 KiB repetition window (and the 256 KiB per-call cap would stop any
// non-repeating payload), the call is never emitted and the client hangs up.
// Legitimate large arguments below the cap (200 KiB of varied CSV) still
// stream through intact on both wires.
func TestA130_HugeToolArgs5MBChunked(t *testing.T) {
	const total = 5 << 20
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			var sent atomic.Int64
			chunk := strings.Repeat("abcdefgh", 1024) // 8 KiB, non-whitespace
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, `{"path":"big.txt","content":"`)
				for sent.Load() < total {
					if !a130ToolArgs(wire, w, chunk) || r.Context().Err() != nil {
						return
					}
					sent.Add(int64(len(chunk)))
				}
				a130ToolArgs(wire, w, `"}`)
				if wire == wireChat {
					a130Write(w, chatFinishLine("tool_calls")+chatDoneLine)
				} else {
					a130Write(w, respCompletedLine(nil))
				}
			})
			p, _ := a130Provider(t, wire, m.URL(), 10*time.Second)
			res := a130Run(t, context.Background(), p, 60*time.Second)
			t.Logf("%s: err=%v calls=%d serverSent=%d elapsed=%s", wire, res.Err, len(res.Calls), sent.Load(), res.Elapsed)
			var runaway *RunawayToolCallError
			if !errors.As(res.Err, &runaway) || runaway.Reason != RunawayRepetition || len(res.Calls) != 0 || runaway.KB > 64 {
				t.Fatalf("5 MB repeating args not stopped early: err=%v calls=%d", res.Err, len(res.Calls))
			}
		})
	}
	var csv strings.Builder
	for i := 0; csv.Len() < 200<<10; i++ {
		fmt.Fprintf(&csv, "%d,2024-%02d-%02d,item-%d,%d.%02d\n", 100000+i, 1+i%12, 1+i%28, i*7, (i*7919)%10000, i%100)
	}
	full := a130JSON(map[string]string{"path": "big.csv", "content": csv.String()})
	for _, wire := range a130Wires {
		t.Run(string(wire)+"/legit-200KiB", func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, full[:1])
				for i := 1; i < len(full); i += 8192 {
					a130ToolArgs(wire, w, full[i:min(i+8192, len(full))])
				}
				if wire == wireChat {
					a130Write(w, chatFinishLine("tool_calls")+chatDoneLine)
				} else {
					a130Write(w, respCompletedLine(nil))
				}
			})
			p, _ := a130Provider(t, wire, m.URL(), 10*time.Second)
			res := a130Run(t, context.Background(), p, 30*time.Second)
			if res.Err != nil || len(res.Calls) != 1 || res.Calls[0].Arguments != full {
				t.Fatalf("legit 200 KiB args: err=%v calls=%d", res.Err, len(res.Calls))
			}
		})
	}
}

// 1h. One SSE line larger than 1 MiB. Real Responses API streams echo the
// full tool arguments on one line (function_call_arguments.done,
// output_item.done, response.completed); many Chat Completions servers send a
// whole call or a long answer in one chunk. The line scanners used to be
// capped at 1 MiB and failed the response with "bufio.Scanner: token too
// long". Now lines are read without that cap: a 1.5 MiB text line is parsed,
// and a 1.5 MiB single-line tool call is rejected by the 256 KiB per-call
// argument cap (typed RunawayToolCallError, retried by the agent), not by the
// line reader.
func TestA130_HugeToolArgsSingleSSELine(t *testing.T) {
	const size = 1536 << 10 // 1.5 MiB
	var text strings.Builder
	for i := 0; text.Len() < size; i++ {
		fmt.Fprintf(&text, "paragraph %d: the quick brown fox %d jumps over %d lazy dogs. ", i, i*31, i%97)
	}
	for _, wire := range a130Wires {
		t.Run(string(wire)+"/text", func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, text.String())
				a130Finish(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 10*time.Second)
			res := a130Run(t, context.Background(), p, 30*time.Second)
			if res.Err != nil || res.Content != text.String() {
				t.Fatalf("1.5 MiB single-line text: err=%v len=%d", res.Err, len(res.Content))
			}
		})
	}
	var content strings.Builder
	for i := 0; content.Len() < size; i++ {
		fmt.Fprintf(&content, "row %d,%d,%d\n", i, i*i, i*7919%10007)
	}
	full := a130JSON(map[string]string{"path": "big.txt", "content": content.String()})
	for _, wire := range a130Wires {
		t.Run(string(wire)+"/tool-args", func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130SSEHeaders(w)
				if wire == wireChat {
					a130Write(w, chatToolLine("call_1", "write_file", full))
					a130Write(w, chatFinishLine("tool_calls")+chatDoneLine)
					return
				}
				// Gateway shape without deltas: the full arguments only on
				// the done / completed lines.
				a130Write(w, respToolAddedLine("write_file"))
				a130Write(w, respToolDoneLines("write_file", full))
				a130Write(w, respCompletedLine([]any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "write_file", "arguments": full}}))
			})
			p, _ := a130Provider(t, wire, m.URL(), 10*time.Second)
			res := a130Run(t, context.Background(), p, 30*time.Second)
			t.Logf("%s: err=%v calls=%d elapsed=%s", wire, res.Err, len(res.Calls), res.Elapsed)
			var runaway *RunawayToolCallError
			if res.Err != nil && strings.Contains(res.Err.Error(), "token too long") {
				t.Fatalf("SSE line limit still applies: %v", res.Err)
			}
			if !errors.As(res.Err, &runaway) || runaway.Reason != RunawayTooLarge || len(res.Calls) != 0 {
				t.Fatalf("1.5 MiB single-line args: want the 256 KiB cap error, got err=%v calls=%d", res.Err, len(res.Calls))
			}
		})
	}
}

// 1i. Degenerate whitespace arguments are stopped by the runaway guard.
// Measures how much the server streamed before the client cancelled.
func TestA130_DegenerateWhitespaceArgsStopped(t *testing.T) {
	for _, wire := range a130Wires {
		for _, variant := range []struct{ name, unit string }{
			{"raw-whitespace", " \n \t "},
			{"json-escapes", `\n\t\n \r\n `},
		} {
			t.Run(string(wire)+"/"+variant.name, func(t *testing.T) {
				var sent atomic.Int64
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
					a130ToolStart(wire, w, `{"path":"a.go","content":"x`)
					block := strings.Repeat(variant.unit, 512/len(variant.unit)+1)
					for i := 0; i < 20000; i++ { // up to ~10 MB
						if !a130ToolArgs(wire, w, block) {
							return
						}
						sent.Add(int64(len(block)))
						if r.Context().Err() != nil {
							return
						}
					}
					a130Finish(wire, w)
				})
				p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
				res := a130Run(t, context.Background(), p, 20*time.Second)
				var runaway *RunawayToolCallError
				t.Logf("%s/%s: err=%v serverSent=%d elapsed=%s", wire, variant.name, res.Err, sent.Load(), res.Elapsed)
				if !errors.As(res.Err, &runaway) || len(res.Calls) != 0 {
					t.Fatalf("whitespace runaway not stopped: err=%v calls=%d", res.Err, len(res.Calls))
				}
				if runaway.KB > 16 {
					t.Fatalf("guard fired late: %d KB", runaway.KB)
				}
			})
		}
	}
}

// a130IncidentBlock is the 92-character repeating unit of the 1.0.28
// incident (gpt-6-luna, v2-go-rename-api-r2, step 6; ~267k chars of
// arguments, 128000 output tokens, 739 s). Only ~17 % of it is whitespace,
// so the whitespace-only runaway guard never fires.
const a130IncidentBlock = " RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t:\\t\" } \ufffc RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t,\\t\" } \ufffc"

// 3. Reproduction of the 1.0.28 degenerate-args incident at wire level:
// a tool call whose arguments degenerate into a repeating NON-whitespace
// block (the incident streamed ~265k chars / 128k tokens for 12 min). The
// repetition guard stops it within the first 16-32 KiB with a typed
// RunawayToolCallError; the call is never emitted.
func TestA130_DegenerateRepeatingBlockArgsStopped(t *testing.T) {
	const limit = 2 << 20
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			var sent atomic.Int64
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, `{"tool":"rename","args":{"from":"Foo","to":"Bar`)
				block := strings.Repeat(a130IncidentBlock, 16)
				for sent.Load() < limit {
					if !a130ToolArgs(wire, w, block) || r.Context().Err() != nil {
						return
					}
					sent.Add(int64(len(block)))
				}
				if wire == wireChat {
					a130Write(w, chatFinishLine("length")+chatDoneLine)
				} else {
					a130Write(w, respLine(map[string]any{"type": "response.incomplete", "response": map[string]any{
						"incomplete_details": map[string]any{"reason": "max_output_tokens"},
						"usage":              map[string]any{"input_tokens": 10, "output_tokens": 128000, "total_tokens": 128010}}}))
				}
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 60*time.Second)
			t.Logf("%s: err=%v finish=%q serverSent=%d acceptedArgBytes=%d elapsed=%s", wire, res.Err, res.Finish, sent.Load(), res.ArgBytes, res.Elapsed)
			var runaway *RunawayToolCallError
			if !errors.As(res.Err, &runaway) || runaway.Reason != RunawayRepetition || len(res.Calls) != 0 || runaway.KB > 48 {
				t.Fatalf("repeating block not stopped: err=%v calls=%d", res.Err, len(res.Calls))
			}
			if res.ArgBytes > 256<<10 {
				t.Fatalf("accepted %d bytes of a repeating block", res.ArgBytes)
			}
		})
	}
}

// 1j. Silent hang: 200 + headers, a little output, then nothing forever.
// The idle watchdog (Timeout) must end the stream.
func TestA130_SilentHangAfterHeaders(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			check := a130GoroutineBaseline(t)
			release := make(chan struct{})
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, "thinking about it")
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			p, tr := a130Provider(t, wire, m.URL(), 1*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			close(release)
			t.Logf("%s: err=%v class=%s elapsed=%s", wire, res.Err, ClassifyProviderError(res.Err), res.Elapsed)
			if res.Err == nil {
				t.Fatalf("silent hang reported success")
			}
			if res.Elapsed < 900*time.Millisecond || res.Elapsed > 3*time.Second {
				t.Fatalf("idle cut after %s, want ~1s", res.Elapsed)
			}
			m.srv.Close()
			check(2, tr.CloseIdleConnections)
		})
	}
}

// 1j'. The idle-timeout error must not look like a user cancel: it is a
// typed StreamIdleTimeoutError ("no data from provider for ..."), not
// context.Canceled, and classified transient so the agent retries it.
func TestA130_SilentHangErrorIsClearAndRetryable(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130SSEHeaders(w)
				<-r.Context().Done()
			})
			p, _ := a130Provider(t, wire, m.URL(), 500*time.Millisecond)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			msg := strings.ToLower(res.Err.Error())
			clear := strings.Contains(msg, "idle") || strings.Contains(msg, "timeout") || strings.Contains(msg, "no data")
			cls := ClassifyProviderError(res.Err)
			t.Logf("%s: err=%q class=%s", wire, res.Err, cls)
			if !clear || cls != RetryTransient {
				t.Fatalf("unclear/non-transient idle-timeout error %q (class %s)", res.Err, cls)
			}
			var idle *StreamIdleTimeoutError
			if !errors.Is(res.Err, ErrStreamIdleTimeout) || !errors.As(res.Err, &idle) || idle.Idle != 500*time.Millisecond || errors.Is(res.Err, context.Canceled) {
				t.Fatalf("idle timeout not typed: %T %v (canceled=%v)", res.Err, res.Err, errors.Is(res.Err, context.Canceled))
			}
		})
	}
}

// 1k. Headers never arrive: bounded by the same Timeout.
func TestA130_NoResponseHeaders(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				<-r.Context().Done()
			})
			p, _ := a130Provider(t, wire, m.URL(), 700*time.Millisecond)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			t.Logf("%s: err=%v class=%s elapsed=%s", wire, res.Err, ClassifyProviderError(res.Err), res.Elapsed)
			if res.Err == nil || !strings.Contains(res.Err.Error(), "response headers timeout") {
				t.Fatalf("want header timeout, got %v", res.Err)
			}
			if res.Elapsed > 2*time.Second {
				t.Fatalf("header timeout took %s", res.Elapsed)
			}
		})
	}
}

// 1l. Slow drip: a byte every 300 ms with a 1 s idle timeout is NOT cut
// (the watchdog resets on every read; by design there is no whole-request
// deadline). A gap longer than the timeout is cut.
func TestA130_SlowDripStream(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				line := chatContentLine("drip")
				if wire == wireResponses {
					line = respTextLine("drip")
				}
				a130SSEHeaders(w)
				gap := 100 * time.Millisecond
				if n == 2 {
					gap = 1500 * time.Millisecond
				}
				// 25 single bytes (2.5 s total, > the 1 s idle timeout), then
				// the rest of the line at once.
				for i := 0; i < 25; i++ {
					if !a130Write(w, line[i:i+1]) || r.Context().Err() != nil {
						return
					}
					time.Sleep(gap)
					if n == 2 && i > 3 {
						return
					}
				}
				a130Write(w, line[25:])
				a130Finish(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 1*time.Second)
			res := a130Run(t, context.Background(), p, 60*time.Second)
			t.Logf("%s drip 1B/100ms: err=%v content=%q elapsed=%s", wire, res.Err, res.Content, res.Elapsed)
			if res.Err != nil || res.Content != "drip" {
				t.Fatalf("slow-but-alive stream was cut: %+v", res)
			}
			res = a130Run(t, context.Background(), p, 20*time.Second)
			t.Logf("%s drip 1B/1.5s: err=%v elapsed=%s", wire, res.Err, res.Elapsed)
			if res.Err == nil || res.Elapsed > 4*time.Second {
				t.Fatalf("gap > idle timeout not cut: %+v", res)
			}
		})
	}
}

// 1l'. Soak: 1 byte / 2 s against the DEFAULT 300 s idle timeout: the stream
// never ends on SuperCli's side (no total deadline). Runs 30 s.
func TestA130_SoakSlowDripNoTotalDeadline(t *testing.T) {
	a130Soak(t)
	m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
		a130SSEHeaders(w)
		line := chatContentLine(strings.Repeat("x", 200))
		for i := 0; i < len(line); i++ {
			if !a130Write(w, line[i:i+1]) || r.Context().Err() != nil {
				return
			}
			time.Sleep(2 * time.Second)
		}
	})
	p, _ := a130Provider(t, wireChat, m.URL(), 0) // 0 -> default 300 s
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := a130Run(t, ctx, p, 40*time.Second)
	t.Logf("drip 1B/2s, default timeout: err=%v elapsed=%s (ended only by the test's 30 s ctx)", res.Err, res.Elapsed)
	if !errors.Is(res.Err, context.DeadlineExceeded) && res.Err != nil && !strings.Contains(res.Err.Error(), "deadline") {
		t.Logf("ended by: %v", res.Err)
	}
}

// 1m. Very long single response: 8 MiB of text in 4 KiB chunks.
func TestA130_VeryLongSingleResponse(t *testing.T) {
	const total = 8 << 20
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			chunk := strings.Repeat("lorem ipsum dolor sit amet, ", 4096/28)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130SSEHeaders(w)
				for sent := 0; sent < total; sent += len(chunk) {
					line := chatContentLine(chunk)
					if wire == wireResponses {
						line = respTextLine(chunk)
					}
					if !a130Write(w, line) {
						return
					}
				}
				a130Finish(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 10*time.Second)
			res := a130Run(t, context.Background(), p, 60*time.Second)
			t.Logf("%s: err=%v bytes=%d deltas=%d elapsed=%s", wire, res.Err, len(res.Content), res.Deltas, res.Elapsed)
			if res.Err != nil || len(res.Content) < total || res.Finish != "stop" {
				t.Fatalf("long response failed: err=%v len=%d finish=%q", res.Err, len(res.Content), res.Finish)
			}
		})
	}
}

// 3'. What output cap does SuperCli request? Before 1.0.30 neither wire sent
// max_tokens / max_completion_tokens / max_output_tokens by default, so a
// degenerate stream ran to the model maximum (128k tokens for gpt-6-luna in
// the 1.0.28 incident). Now a cloud endpoint gets DefaultMaxOutputTokens
// unless SUPERCLI_LLM_MAX_TOKENS (MaxTokens) is configured; local servers get
// nothing (their limit is the loaded context).
func TestA130_RequestedOutputTokenCap(t *testing.T) {
	capFields := func(body map[string]any) map[string]any {
		found := map[string]any{}
		for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if v, ok := body[k]; ok {
				found[k] = v
			}
		}
		return found
	}
	for _, wire := range a130Wires {
		field := "max_tokens"
		if wire == wireResponses {
			field = "max_output_tokens"
		}
		for _, tc := range []struct {
			name       string
			remote     bool
			configured int
			want       any
		}{
			{"cloud-default", true, 0, float64(DefaultMaxOutputTokens)},
			{"cloud-configured", true, 1234, float64(1234)},
			{"local-default", false, 0, nil},
			{"local-configured", false, 777, float64(777)},
		} {
			t.Run(string(wire)+"/"+tc.name, func(t *testing.T) {
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) { a130OK(wire, w) })
				p := a130CapProvider(t, wire, m, tc.remote, "cap-model-"+tc.name, tc.configured)
				if res := a130Run(t, context.Background(), p, 10*time.Second); res.Err != nil {
					t.Fatal(res.Err)
				}
				found := capFields(m.Body(0))
				t.Logf("%s/%s: output cap fields in request: %v", wire, tc.name, found)
				if tc.want == nil {
					if len(found) != 0 {
						t.Fatalf("local default request carries an output cap %v", found)
					}
					return
				}
				if len(found) != 1 || found[field] != tc.want {
					t.Fatalf("want %s=%v, got %v", field, tc.want, found)
				}
			})
		}
	}
}

// A provider that rejects the default cap (too large for the model, or an
// unknown parameter) gets one retry with the limit it named or without the
// field; the decision is remembered for the next request.
func TestA130_DefaultOutputCapRejectionIsLearned(t *testing.T) {
	for _, wire := range a130Wires {
		for _, tc := range []struct {
			name, body string
			want       any
		}{
			{"too-large", `{"error":{"message":"max_tokens is too large: 32000. This model supports at most 16384 completion tokens, whereas you provided 32000.","type":"invalid_request_error","param":"max_tokens","code":"invalid_value"}}`, float64(16384)},
			{"unsupported", `{"error":{"message":"Unsupported parameter: 'max_output_tokens' / 'max_tokens' is not supported with this model.","type":"invalid_request_error","code":"unsupported_parameter"}}`, nil},
		} {
			t.Run(string(wire)+"/"+tc.name, func(t *testing.T) {
				a130WithRetries(t, 0, false)
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
					if n == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprint(w, tc.body)
						return
					}
					a130OK(wire, w)
				})
				model := "learn-" + string(wire) + "-" + tc.name
				p := a130CapProvider(t, wire, m, true, model, 0)
				for i := 0; i < 2; i++ {
					if res := a130Run(t, context.Background(), p, 10*time.Second); res.Err != nil || res.Content != "ok" {
						t.Fatalf("run %d: err=%v content=%q", i, res.Err, res.Content)
					}
				}
				if got := m.requests.Load(); got != 3 {
					t.Fatalf("requests = %d, want 3 (rejected, retried, learned)", got)
				}
				for _, i := range []int{1, 2} {
					body := m.Body(i)
					var got any
					for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
						if v, ok := body[k]; ok {
							got = v
						}
					}
					if got != tc.want {
						t.Fatalf("request %d cap = %v, want %v", i+1, got, tc.want)
					}
				}
			})
		}
	}
}

// a130CapProvider builds the provider against the mock either as a local
// endpoint (127.0.0.1) or under a public-looking hostname that a custom
// dialer routes to the mock, so the cloud defaults apply.
func a130CapProvider(t *testing.T, wire a130Wire, m *a130Mock, remote bool, model string, maxTokens int) Provider {
	t.Helper()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(tr.CloseIdleConnections)
	baseURL := m.URL()
	if remote {
		addr := strings.TrimPrefix(m.srv.URL, "http://")
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		}
		baseURL = "http://api.cloud-provider.example/v1"
	}
	client := &http.Client{Transport: tr}
	var (
		p   Provider
		err error
	)
	if wire == wireChat {
		p, err = NewOpenAI(OpenAIConfig{BaseURL: baseURL, APIKey: "dummy", Model: model, MaxTokens: maxTokens, Timeout: 5 * time.Second, HTTPClient: client, Capabilities: NewCapabilityRegistry()})
	} else {
		p, err = NewResponses(ResponsesConfig{BaseURL: baseURL, APIKey: "dummy", Model: model, MaxTokens: maxTokens, Timeout: 5 * time.Second, HTTPClient: client, Capabilities: NewCapabilityRegistry()})
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}
