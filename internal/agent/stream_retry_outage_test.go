package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// No sockets or real sleeps: advance only the retry scheduler's clock.
func fakeOutageClock(t *testing.T) (*time.Time, *[]time.Duration) {
	t.Helper()
	savedNow, savedWait := streamRetryNow, streamRetryWait
	now := time.Unix(1000, 0)
	var waits []time.Duration
	streamRetryNow = func() time.Time { return now }
	streamRetryWait = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		waits = append(waits, d)
		now = now.Add(d)
		return nil
	}
	t.Cleanup(func() { streamRetryNow, streamRetryWait = savedNow, savedWait })
	return &now, &waits
}

func outageError(code syscall.Errno) error {
	return fmt.Errorf("request: %w", &url.Error{Op: "Post", Err: &net.OpError{Op: "dial", Net: "tcp", Err: code}})
}

func TestOutageRetryRecoversBeyondOldBudget(t *testing.T) {
	_, waits := fakeOutageClock(t)
	p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
		if call < 5 {
			return nil, outageError(syscall.ENETDOWN)
		}
		return startupRetryStream(llm.Delta{Content: "recovered", FinishReason: "stop"}), nil
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	out := make(chan Event, 32)
	text, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), out)
	if err != nil || text != "recovered" || p.calls.Load() != 5 {
		t.Fatalf("text=%q err=%v calls=%d", text, err, p.calls.Load())
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}; !reflect.DeepEqual(*waits, want) {
		t.Fatalf("waits=%v want=%v", *waits, want)
	}
	close(out)
	notices := 0
	for ev := range out {
		if e, ok := ev.(NoticeEvent); ok {
			notices++
			if !strings.Contains(e.Text, "waiting") || strings.Contains(e.Text, "dial") {
				t.Fatalf("not a quiet waiting notice: %q", e.Text)
			}
		}
	}
	if notices != 4 {
		t.Fatalf("notices=%d", notices)
	}
}

func TestOutageRetryFiniteExhaustion(t *testing.T) {
	_, waits := fakeOutageClock(t)
	cause := outageError(syscall.ENETUNREACH)
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, cause }}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
	if !errors.Is(err, cause) || p.calls.Load() != 6 {
		t.Fatalf("err=%v calls=%d", err, p.calls.Load())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	if !reflect.DeepEqual(*waits, want) {
		t.Fatalf("waits=%v want=%v", *waits, want)
	}
}

func TestOutageRetryWindowIncludesAttempts(t *testing.T) {
	now, waits := fakeOutageClock(t)
	cause := outageError(syscall.ECONNRESET)
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
		*now = now.Add(20 * time.Second)
		return nil, cause
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
	if !errors.Is(err, cause) || p.calls.Load() != 3 || len(*waits) != 2 {
		t.Fatalf("err=%v calls=%d waits=%v", err, p.calls.Load(), *waits)
	}
}

func TestOutageRetryCancelDeadlineAndExpiredWindow(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "window"} {
		t.Run(mode, func(t *testing.T) {
			now, _ := fakeOutageClock(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := outageError(syscall.ENETDOWN)
			streamRetryWait = func(context.Context, time.Duration) error {
				switch mode {
				case "cancel":
					cancel()
					return ctx.Err()
				case "deadline":
					return context.DeadlineExceeded
				default:
					*now = now.Add(31 * time.Second)
					return nil
				}
			}
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, cause }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(ctx, l.prepareToolRequest(), make(chan Event, 32))
			want := cause
			if mode == "cancel" {
				want = context.Canceled
			}
			if mode == "deadline" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || p.calls.Load() != 1 {
				t.Fatalf("err=%v want=%v calls=%d", err, want, p.calls.Load())
			}
		})
	}
}

func TestOutageRetryTypedClassification(t *testing.T) {
	for name, cause := range map[string]error{
		"down": outageError(syscall.ENETDOWN), "unreachable": outageError(syscall.ENETUNREACH),
		"host-unreachable": outageError(syscall.EHOSTUNREACH), "reset": outageError(syscall.ECONNRESET),
		"temporary-dns": startupTemporaryError(),
		"timeout":       &url.Error{Op: "Post", Err: &net.DNSError{IsTimeout: true}},
	} {
		t.Run(name, func(t *testing.T) {
			fakeOutageClock(t)
			p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
				if call < 4 {
					return nil, cause
				}
				return startupRetryStream(llm.Delta{Content: "ok", FinishReason: "stop"}), nil
			}}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
			if err != nil || p.calls.Load() != 4 {
				t.Fatalf("err=%v calls=%d", err, p.calls.Load())
			}
		})
	}
}

func TestOutageRetryExclusionsAndUnchangedGenericBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		cause error
		calls int
	}{
		"generic-eof":   {io.EOF, 3},
		"http2":         {&url.Error{Op: "Post", Err: startupHTTP2TransportError("http2: server sent GOAWAY")}, 3},
		"429":           {&llm.HTTPResponseError{Status: 429, Body: "connection reset by peer"}, 1},
		"503":           {&llm.HTTPResponseError{Status: 503, Body: "unexpected EOF"}, 1},
		"auth":          {&url.Error{Op: "Post", Err: &llm.HTTPResponseError{Status: 401}}, 1},
		"payload":       {&llm.HTTPResponseError{Status: 400}, 1},
		"permanent-dns": {&url.Error{Op: "Post", Err: &net.DNSError{IsNotFound: true, IsTemporary: true}}, 1},
		"tls":           {&url.Error{Op: "Post", Err: &tls.CertificateVerificationError{Err: io.EOF}}, 1},
		"certificate":   {&net.OpError{Op: "dial", Err: x509.UnknownAuthorityError{}}, 1},
		"invalid-input": {errors.New("invalid argument: network is down"), 1},
		"plain-reset":   {errors.New("connection reset by peer"), 1},
		"bare-deadline": {context.DeadlineExceeded, 1},
		"refused":       {outageError(syscall.ECONNREFUSED), 1},
	} {
		t.Run(name, func(t *testing.T) {
			fakeOutageClock(t)
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, tc.cause }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
			if !errors.Is(err, tc.cause) || p.calls.Load() != int32(tc.calls) {
				t.Fatalf("err=%v calls=%d want=%d", err, p.calls.Load(), tc.calls)
			}
		})
	}
}

func TestOutageRetryDoesNotExpandMixedGenericBudget(t *testing.T) {
	fakeOutageClock(t)
	p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
		if call < 4 {
			return nil, outageError(syscall.ENETDOWN)
		}
		return nil, io.EOF
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
	if !errors.Is(err, io.EOF) || p.calls.Load() != 4 {
		t.Fatalf("err=%v calls=%d", err, p.calls.Load())
	}
}

func TestOutageRetryWindowsSocketErrors(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Winsock errors are Windows-specific")
	}
	for _, code := range []syscall.Errno{10050, 10051, 10052, 10053, 10054, 10060, 10065} {
		err := &providerCompleteError{cause: outageError(code)}
		if !isShortNetworkOutage(err) {
			t.Errorf("Winsock %d was not recognized", code)
		}
	}
	if isShortNetworkOutage(&providerCompleteError{cause: outageError(syscall.Errno(10061))}) {
		t.Fatal("Winsock connection refused must not get outage recovery")
	}
}

func TestOutageRetryDoesNotRepeatSideEffects(t *testing.T) {
	for _, afterTool := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-tool=%v", afterTool), func(t *testing.T) {
			fakeOutageClock(t)
			var mutations atomic.Int32
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{
				Name: "fixture_mutation", Description: "in-memory fixture only", Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					mutations.Add(1)
					return tools.Result{Text: "fixture changed"}, nil
				},
			})
			p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
				toolCall := 5
				if afterTool {
					toolCall = 1
				}
				if call == toolCall {
					return startupRetryStream(llm.Delta{ToolCall: &llm.ToolCall{ID: "one", Name: "fixture_mutation", Arguments: `{}`}}, llm.Delta{FinishReason: "tool_calls"}), nil
				}
				if call < 6 {
					return nil, outageError(syscall.ENETDOWN)
				}
				return startupRetryStream(llm.Delta{Content: "done", FinishReason: "stop"}), nil
			}}
			l := makeLoop(t, p, reg, "")
			ch, err := l.Run(context.Background(), "perform the fixture mutation once")
			if err != nil {
				t.Fatal(err)
			}
			done := false
			for _, event := range drainEvents(t, ch) {
				switch e := event.(type) {
				case ErrorEvent:
					t.Fatalf("run failed: %v", e.Err)
				case DoneEvent:
					done = true
				}
			}
			if !done || mutations.Load() != 1 || p.calls.Load() != 6 {
				t.Fatalf("done=%v mutations=%d calls=%d", done, mutations.Load(), p.calls.Load())
			}
		})
	}
}

func TestOutageRetryInterruptedReplyIsRetainedWithoutExecutingCalls(t *testing.T) {
	fakeOutageClock(t)
	var mutations atomic.Int32
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "fixture_mutation", Description: "fixture only", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		mutations.Add(1)
		return tools.Result{Text: "changed"}, nil
	}})
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
		return startupRetryStream(llm.Delta{Reasoning: "partial reasoning"}, llm.Delta{Content: "partial answer"}, llm.Delta{ToolCall: &llm.ToolCall{ID: "one", Name: "fixture_mutation", Arguments: `{}`}}, llm.Delta{Err: outageError(syscall.ECONNRESET)}), nil
	}}
	w := &recordingWriter{}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "perform the fixture mutation once")
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, ev := range drainEvents(t, ch) {
		if _, ok := ev.(ErrorEvent); ok {
			failed = true
		}
	}
	if !failed || mutations.Load() != 0 || p.calls.Load() != 1 {
		t.Fatalf("failed=%v mutations=%d calls=%d", failed, mutations.Load(), p.calls.Load())
	}
	if !strings.Contains(reqText(l.Messages), "partial answer") || strings.Contains(reqText(l.Messages), "partial reasoning") {
		t.Fatal("partial answer was not retained or reasoning leaked into provider history")
	}
	if !strings.Contains(reqText(w.messages), "partial answer") || !strings.Contains(reqText(w.messages), "partial reasoning") {
		t.Fatal("interrupted displayed transcript was not persisted")
	}
	for _, msg := range l.Messages {
		if len(msg.ToolCalls) > 0 {
			t.Fatal("unexecuted tool calls must not create dangling history")
		}
	}
}

func TestOutageRetryNeverReplaysAcceptedStream(t *testing.T) {
	for name, frame := range map[string]llm.Delta{
		"content": {Content: "partial answer"}, "whitespace": {Content: " "},
		"reasoning": {Reasoning: "partial reasoning"}, "native": {NativeReasoning: &llm.ReasoningBlock{}},
		"tool":       {ToolCall: &llm.ToolCall{ID: "one", Name: "fixture_mutation", Arguments: `{}`}},
		"tool-start": {OutputStarted: true}, "reasoning-start": {ReasoningStarted: true},
		"empty-error": {},
	} {
		for _, sameFrame := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/same-frame=%v", name, sameFrame), func(t *testing.T) {
				frame := frame
				fakeOutageClock(t)
				cause := errHTTP2Reset
				p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
					if sameFrame {
						frame.Err = cause
						return startupRetryStream(frame), nil
					}
					return startupRetryStream(frame, llm.Delta{Err: cause}), nil
				}}
				l := makeLoop(t, p, tools.NewRegistry(), "")
				_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
				if !errors.Is(err, cause) || p.calls.Load() != 1 {
					t.Fatalf("err=%v calls=%d", err, p.calls.Load())
				}
			})
		}
	}
}
