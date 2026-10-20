package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestOutageReviewAcceptedStreamContextOverflowNoReplay(t *testing.T) {
	for _, mode := range []string{"content", "reasoning"} {
		for _, completedTool := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/completed-tool=%v", mode, completedTool), func(t *testing.T) {
				fakeOutageClock(t)
				var completed, pending atomic.Int32
				reg := tools.NewRegistry()
				for name, count := range map[string]*atomic.Int32{"fixture_completed": &completed, "fixture_pending": &pending} {
					reg.MustRegister(tools.Tool{Name: name, Description: "in-memory fixture", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						count.Add(1)
						return tools.Result{Text: "fixture changed"}, nil
					}})
				}
				partialCall := 1
				if completedTool {
					partialCall = 2
				}
				cause := errors.New("maximum context length exceeded")
				p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
					if completedTool && call == 1 {
						return startupRetryStream(llm.Delta{ToolCall: &llm.ToolCall{ID: "completed", Name: "fixture_completed", Arguments: `{}`}}, llm.Delta{FinishReason: "tool_calls"}), nil
					}
					if call == partialCall {
						var frames []llm.Delta
						if mode == "content" {
							frames = append(frames, llm.Delta{Content: "retained partial answer"})
						}
						if mode == "reasoning" {
							frames = append(frames, llm.Delta{Reasoning: "retained partial reasoning"})
						}
						if mode != "error-only" {
							frames = append(frames, llm.Delta{ToolCall: &llm.ToolCall{ID: "pending", Name: "fixture_pending", Arguments: `{}`}})
						}
						return startupRetryStream(append(frames, llm.Delta{Err: cause})...), nil
					}
					return startupRetryStream(llm.Delta{Content: "unsafe replacement", FinishReason: "stop"}), nil
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
				failed, done := false, false
				for _, event := range drainEvents(t, ch) {
					switch e := event.(type) {
					case ErrorEvent:
						failed = errors.Is(e.Err, cause)
					case DoneEvent:
						done = true
					}
				}
				wantCompleted := int32(partialCall - 1)
				if !failed || done || p.calls.Load() != int32(partialCall) || completed.Load() != wantCompleted || pending.Load() != 0 {
					t.Fatalf("failed=%v done=%v calls=%d want=%d completed=%d pending=%d", failed, done, p.calls.Load(), partialCall, completed.Load(), pending.Load())
				}
				if mode == "content" && (!strings.Contains(reqText(l.Messages), "retained partial answer") || !strings.Contains(reqText(w.messages), "retained partial answer")) {
					t.Fatal("partial content was replaced or not retained")
				}
				if mode == "reasoning" && !strings.Contains(reqText(w.messages), "retained partial reasoning") {
					t.Fatal("partial reasoning was replaced or not persisted")
				}
				if strings.Contains(reqText(l.Messages), "unsafe replacement") || strings.Contains(reqText(w.messages), "unsafe replacement") {
					t.Fatal("replacement completion entered history")
				}
			})
		}
	}
}

func TestOutageReviewAcceptedContextProgressSignals(t *testing.T) {
	for name, frame := range map[string]llm.Delta{
		"content": {Content: "partial"}, "whitespace": {Content: " "},
		"reasoning": {Reasoning: "partial reasoning"}, "tool-start": {OutputStarted: true},
		"reasoning-start": {ReasoningStarted: true}, "native": {NativeReasoning: &llm.ReasoningBlock{}},
		"tool": {ToolCall: &llm.ToolCall{ID: "pending", Name: "fixture_pending", Arguments: `{}`}},
	} {
		for _, sameFrame := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/same-frame=%v", name, sameFrame), func(t *testing.T) {
				frame := frame
				fakeOutageClock(t)
				cause := errors.New("maximum context length exceeded")
				p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
					if call > 1 {
						return startupRetryStream(llm.Delta{Content: "unsafe replacement", FinishReason: "stop"}), nil
					}
					if sameFrame {
						frame.Err = cause
						return startupRetryStream(frame), nil
					}
					return startupRetryStream(frame, llm.Delta{Err: cause}), nil
				}}
				var pending atomic.Int32
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{Name: "fixture_pending", Description: "fixture only", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					pending.Add(1)
					return tools.Result{Text: "changed"}, nil
				}})
				l := makeLoop(t, p, reg, "")
				ch, err := l.Run(context.Background(), "fixture request")
				if err != nil {
					t.Fatal(err)
				}
				failed, done := false, false
				for _, event := range drainEvents(t, ch) {
					switch e := event.(type) {
					case ErrorEvent:
						failed = errors.Is(e.Err, cause)
					case DoneEvent:
						done = true
					}
				}
				if !failed || done || p.calls.Load() != 1 || pending.Load() != 0 {
					t.Fatalf("failed=%v done=%v calls=%d pending=%d", failed, done, p.calls.Load(), pending.Load())
				}
			})
		}
	}
}

func TestOutageReviewErrorOnlyContextRejectionStillCompacts(t *testing.T) {
	fakeOutageClock(t)
	p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
		if call == 1 {
			return startupRetryStream(llm.Delta{Err: errors.New("context length is 4096 tokens: too many tokens")}), nil
		}
		return startupRetryStream(llm.Delta{Content: "repaired response", FinishReason: "stop"}), nil
	}}
	history := echoSummaryHistory()
	window, summaries := 50_000, 0
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: tools.NewRegistry(), InitialMessages: history[:len(history)-1],
		WindowFor: func(string) int { return window }, LearnLimit: func(_ string, n int) { window = n },
		PruneProtectTokens: -1,
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			summaries++
			return WrapCompactSummary("Goal: preserve earlier requirements. Done: earlier inspection. Pending: current task."), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	ch, err := l.Run(context.Background(), "current task")
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for _, event := range drainEvents(t, ch) {
		switch e := event.(type) {
		case ErrorEvent:
			t.Fatalf("context repair failed: %v", e.Err)
		case DoneEvent:
			done = true
		}
	}
	if !done || p.calls.Load() != 2 || summaries != 1 || window != 4096 {
		t.Fatalf("done=%v calls=%d summaries=%d window=%d", done, p.calls.Load(), summaries, window)
	}
}

func TestOutageReviewErrorOnlyNetworkDoesNotRetryFullRun(t *testing.T) {
	fakeOutageClock(t)
	cause := outageError(syscall.ENETDOWN)
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return startupRetryStream(llm.Delta{Err: cause}), nil }}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	ch, err := l.Run(context.Background(), "fixture request")
	if err != nil {
		t.Fatal(err)
	}
	failed, done := false, false
	for _, event := range drainEvents(t, ch) {
		switch e := event.(type) {
		case ErrorEvent:
			failed = errors.Is(e.Err, cause)
		case DoneEvent:
			done = true
		}
	}
	if !failed || done || p.calls.Load() != 1 {
		t.Fatalf("failed=%v done=%v calls=%d", failed, done, p.calls.Load())
	}
}

func TestOutageReviewWrappedTransportErrors(t *testing.T) {
	temporaryDNS := &net.DNSError{IsTemporary: true}
	timeoutDNS := &net.DNSError{IsTimeout: true}
	permanentDNS := &net.DNSError{IsNotFound: true, IsTemporary: true}
	wrapURL := func(err error) error { return &url.Error{Op: "Post", Err: err} }
	wrapOp := func(err error) error { return &net.OpError{Op: "dial", Net: "tcp", Err: err} }
	tests := map[string]struct {
		cause error
		calls int32
	}{
		"url-fmt-op-timeout":          {wrapURL(fmt.Errorf("dial: %w", wrapOp(timeoutDNS))), 6},
		"url-fmt-temporary":           {wrapURL(fmt.Errorf("dial: %w", temporaryDNS)), 6},
		"op-fmt-timeout":              {wrapOp(fmt.Errorf("lookup: %w", timeoutDNS)), 6},
		"op-fmt-temporary":            {wrapOp(fmt.Errorf("lookup: %w", temporaryDNS)), 6},
		"url-joined-temporary":        {wrapURL(errors.Join(errors.New("dial diagnostic"), temporaryDNS)), 6},
		"op-joined-timeout":           {wrapOp(errors.Join(errors.New("dial diagnostic"), timeoutDNS)), 6},
		"url-joined-wrapped-op":       {wrapURL(errors.Join(errors.New("dial diagnostic"), fmt.Errorf("lookup: %w", wrapOp(timeoutDNS)))), 6},
		"op-joined-wrapped-url":       {wrapOp(errors.Join(errors.New("dial diagnostic"), fmt.Errorf("lookup: %w", wrapURL(temporaryDNS)))), 6},
		"bare-joined-dns":             {errors.Join(errors.New("not a transport"), temporaryDNS), 1},
		"unrelated-url-sibling":       {errors.Join(wrapURL(errors.New("invalid argument")), temporaryDNS), 1},
		"unrelated-op-sibling":        {errors.Join(wrapOp(errors.New("invalid argument")), timeoutDNS), 1},
		"unrelated-url-errno-sibling": {errors.Join(wrapURL(errors.New("invalid argument")), syscall.ENETDOWN), 1},
		"unrelated-op-errno-sibling":  {errors.Join(wrapOp(errors.New("invalid argument")), syscall.ECONNRESET), 1},
		"url-fmt-client-deadline":     {wrapURL(fmt.Errorf("client timeout: %w", context.DeadlineExceeded)), 6},
		"bare-wrapped-deadline":       {fmt.Errorf("caller deadline: %w", context.DeadlineExceeded), 1},
	}
	for name, permanent := range map[string]error{
		"permanent-dns": permanentDNS,
		"unflagged-dns": &net.DNSError{},
		"auth":          &llm.HTTPResponseError{Status: 401},
		"tls":           &tls.CertificateVerificationError{Err: timeoutDNS},
		"certificate":   x509.UnknownAuthorityError{},
		"canceled":      context.Canceled,
	} {
		for _, reversed := range []bool{false, true} {
			left, right := error(temporaryDNS), permanent
			if reversed {
				left, right = right, left
			}
			joined := errors.Join(fmt.Errorf("first: %w", left), fmt.Errorf("second: %w", right))
			for scope, cause := range map[string]error{
				"inside-url": wrapURL(joined),
				"inside-op":  wrapOp(joined),
				"outer-join": errors.Join(wrapURL(temporaryDNS), permanent),
			} {
				tests[fmt.Sprintf("mixed-%s/%s/reversed=%v", name, scope, reversed)] = struct {
					cause error
					calls int32
				}{cause, 1}
			}
		}
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, waits := fakeOutageClock(t)
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return nil, tc.cause }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
			if !errors.Is(err, tc.cause) || p.calls.Load() != tc.calls || len(*waits) != int(tc.calls)-1 {
				t.Fatalf("err=%v calls=%d want=%d waits=%v", err, p.calls.Load(), tc.calls, *waits)
			}
		})
	}
}
