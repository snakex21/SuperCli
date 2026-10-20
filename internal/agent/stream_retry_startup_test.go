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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// This provider never opens a socket; startup errors have no response channel.
type startupRetryProvider struct {
	calls atomic.Int32
	run   func(int) (<-chan llm.Delta, error)
}

func (*startupRetryProvider) Name() string { return "startup-fixture" }
func (p *startupRetryProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	return p.run(int(p.calls.Add(1)))
}

func startupRetryStream(deltas ...llm.Delta) <-chan llm.Delta {
	ch := make(chan llm.Delta, len(deltas))
	for _, d := range deltas {
		ch <- d
	}
	close(ch)
	return ch
}

func startupTemporaryError() error {
	return fmt.Errorf("startup: %w", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "fixture temporary transport failure", IsTemporary: true}})
}

func TestStreamRetryStartupRecoversAndMutatesExactlyOnce(t *testing.T) {
	fastStreamRetry(t)
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
		switch call {
		case 1:
			return nil, startupTemporaryError()
		case 2:
			return startupRetryStream(llm.Delta{ToolCall: &llm.ToolCall{ID: "fixture-1", Name: "fixture_mutation", Arguments: `{}`}}, llm.Delta{FinishReason: "tool_calls"}), nil
		default:
			return startupRetryStream(llm.Delta{Content: "fixture complete"}, llm.Delta{FinishReason: "stop"}), nil
		}
	}}
	l := makeLoop(t, p, reg, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, "perform the fixture mutation once")
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for _, ev := range drainEvents(t, ch) {
		switch e := ev.(type) {
		case ErrorEvent:
			t.Fatalf("run failed: %v", e.Err)
		case DoneEvent:
			done = true
		}
	}
	if !done || p.calls.Load() != 3 || mutations.Load() != 1 {
		t.Fatalf("done=%v provider calls=%d mutations=%d", done, p.calls.Load(), mutations.Load())
	}
}

// Mirrors the Error-only shape of net/http's HTTP/2 stream/GOAWAY errors:
// unlike net.OpError, these errors need not implement net.Error.
type startupHTTP2TransportError string

func (e startupHTTP2TransportError) Error() string { return string(e) }

func TestStreamRetryStartupHTTP2TransportCompatibility(t *testing.T) {
	for _, marker := range []string{
		"stream error: stream ID 49; NO_ERROR; received from peer",
		"http2: server sent GOAWAY and closed the connection",
		"http2: client connection lost",
		"http2: response body closed",
	} {
		transport := startupHTTP2TransportError(marker)
		var netErr net.Error
		if errors.As(transport, &netErr) {
			t.Fatal("fixture must exercise an HTTP/2 error without net.Error")
		}
		for name, wrap := range map[string]func(error) error{
			"url": func(err error) error { return &url.Error{Op: "Post", URL: "http://fixture.invalid", Err: err} },
			"op":  func(err error) error { return &net.OpError{Op: "read", Net: "tcp", Err: err} },
			"url-op": func(err error) error {
				return &url.Error{Op: "Post", Err: &net.OpError{Op: "read", Net: "tcp", Err: err}}
			},
		} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				fastStreamRetry(t)
				cause := fmt.Errorf("wrapped: %w", wrap(transport))
				p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
					if call == 1 {
						return nil, cause
					}
					return startupRetryStream(llm.Delta{Content: "recovered"}, llm.Delta{FinishReason: "stop"}), nil
				}}
				l := makeLoop(t, p, tools.NewRegistry(), "")
				text, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
				if err != nil || text != "recovered" || p.calls.Load() != 2 {
					t.Fatalf("text=%q err=%v calls=%d", text, err, p.calls.Load())
				}
			})
		}
	}
}

func TestStreamRetryStartupTypedTransportCases(t *testing.T) {
	for name, cause := range map[string]error{
		"temporary":       startupTemporaryError(),
		"reset":           fmt.Errorf("wrapped: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}),
		"client-timeout":  &url.Error{Op: "Post", URL: "http://fixture.invalid", Err: &net.DNSError{Err: "fixture timeout", IsTimeout: true}},
		"client-deadline": &url.Error{Op: "Post", URL: "http://fixture.invalid", Err: context.DeadlineExceeded},
		"eof":             fmt.Errorf("wrapped: %w", io.EOF),
		"unexpected-eof":  fmt.Errorf("wrapped: %w", io.ErrUnexpectedEOF),
	} {
		t.Run(name, func(t *testing.T) {
			fastStreamRetry(t)
			p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
				if call == 1 {
					return nil, cause
				}
				return startupRetryStream(llm.Delta{Content: "ok"}, llm.Delta{FinishReason: "stop"}), nil
			}}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			text, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
			if err != nil || text != "ok" || p.calls.Load() != 2 {
				t.Fatalf("text=%q err=%v calls=%d", text, err, p.calls.Load())
			}
		})
	}
}

func TestStreamRetryStartupGenericBudgetAndAcceptedStreamBoundary(t *testing.T) {
	fastStreamRetry(t)
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint("mixed-stream=", mixed), func(t *testing.T) {
			cause := io.EOF
			p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
				if mixed && call == 2 {
					return startupRetryStream(llm.Delta{Err: errHTTP2Reset}), nil
				}
				return nil, cause
			}}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
			wantCalls := int32(3)
			if mixed {
				wantCalls, cause = 2, errHTTP2Reset
			}
			if !errors.Is(err, cause) || p.calls.Load() != wantCalls {
				t.Fatalf("err=%v cause retained=%v calls=%d want=%d", err, errors.Is(err, cause), p.calls.Load(), wantCalls)
			}
		})
	}
}

func TestStreamRetryStartupCancelOrDeadlineDuringWait(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			saved := streamRetryDelays
			streamRetryDelays = []time.Duration{time.Hour, time.Hour}
			t.Cleanup(func() { streamRetryDelays = saved })
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, startupTemporaryError() }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			out := make(chan Event)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				l.completeWithStreamRetry(ctx, l.prepareToolRequest(), out)
			}()
			select {
			case ev := <-out:
				if _, ok := ev.(NoticeEvent); !ok {
					t.Errorf("expected retry notice, got %T", ev)
				}
				if !deadline {
					cancel()
				}
			case <-time.After(time.Second):
				cancel()
				t.Error("no retry notice")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("retry wait ignored cancellation/deadline")
			}
			if p.calls.Load() != 1 || ctx.Err() == nil {
				t.Fatalf("calls=%d context=%v", p.calls.Load(), ctx.Err())
			}
		})
	}
}

func TestStreamRetryStartupRejectsPermanentErrors(t *testing.T) {
	for name, cause := range map[string]error{
		"config":                  errors.New("invalid provider configuration"),
		"auth":                    &llm.HTTPResponseError{Status: 401, Body: "unauthorized"},
		"payload":                 &llm.HTTPResponseError{Status: 400, Body: "invalid payload"},
		"forbidden":               &llm.HTTPResponseError{Status: 403},
		"tls-cert":                &url.Error{Op: "Post", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}},
		"tls-cert-with-eof-cause": &tls.CertificateVerificationError{Err: io.EOF},
		"tls-record":              &url.Error{Op: "Post", Err: tls.RecordHeaderError{Msg: "fixture invalid TLS record"}},
		"canceled":                context.Canceled,
		"deadline":                context.DeadlineExceeded,
		"misleading-auth":         errors.New("http 401: unexpected EOF"),
		"plain-http2":             startupHTTP2TransportError("http2: server sent GOAWAY and closed the connection"),
		"url-auth-http2":          &url.Error{Op: "Post", Err: &llm.HTTPResponseError{Status: 401, Body: "http2: server sent GOAWAY"}},
		"url-payload-http2":       &url.Error{Op: "Post", Err: &llm.HTTPResponseError{Status: 400, Body: "stream error: stream ID 49"}},
		"url-string-auth-http2":   &url.Error{Op: "Post", Err: errors.New("http 401: http2: server sent GOAWAY")},
		"url-sse":                 &url.Error{Op: "Post", Err: errors.New("sse: empty stream")},
		"tls-http2-cause":         &url.Error{Op: "Post", Err: &tls.CertificateVerificationError{Err: startupHTTP2TransportError("http2: server sent GOAWAY")}},
	} {
		t.Run(name, func(t *testing.T) {
			fastStreamRetry(t)
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, cause }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
			if !errors.Is(err, cause) || err.Error() != "agent: provider.Complete: "+cause.Error() || p.calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, p.calls.Load())
			}
		})
	}
}

func TestStreamRetryStartupDoesNotRetryWhenChannelReturned(t *testing.T) {
	fastStreamRetry(t)
	cause := startupTemporaryError()
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
		return startupRetryStream(llm.Delta{Content: "already accepted"}), cause
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
	if !errors.Is(err, cause) || p.calls.Load() != 1 {
		t.Fatalf("channel plus startup error was replayed: err=%v calls=%d", err, p.calls.Load())
	}
}

func TestStreamRetryStartupDoesNotReplayCompletedMutation(t *testing.T) {
	fastStreamRetry(t)
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
		switch call {
		case 1:
			return startupRetryStream(llm.Delta{ToolCall: &llm.ToolCall{ID: "fixture-1", Name: "fixture_mutation", Arguments: `{}`}}, llm.Delta{FinishReason: "tool_calls"}), nil
		case 2:
			// The mutation has already happened. Retry only this completion,
			// never the tool batch or the whole agent run.
			return nil, startupTemporaryError()
		default:
			return startupRetryStream(llm.Delta{Content: "fixture complete"}, llm.Delta{FinishReason: "stop"}), nil
		}
	}}
	l := makeLoop(t, p, reg, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, "perform the fixture mutation once")
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for _, ev := range drainEvents(t, ch) {
		switch e := ev.(type) {
		case ErrorEvent:
			t.Fatalf("run failed: %v", e.Err)
		case DoneEvent:
			done = true
		}
	}
	if !done || p.calls.Load() != 3 || mutations.Load() != 1 {
		t.Fatalf("done=%v provider calls=%d mutations=%d", done, p.calls.Load(), mutations.Load())
	}
}

func TestStreamRetryStartupTypesDoNotReclassifyStreamErrors(t *testing.T) {
	fastStreamRetry(t)
	cause := startupTemporaryError()
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
		return startupRetryStream(llm.Delta{Err: cause}), nil
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 16))
	if !errors.Is(err, cause) || p.calls.Load() != 1 {
		t.Fatalf("stream transport type incorrectly received startup policy: err=%v calls=%d", err, p.calls.Load())
	}
}
