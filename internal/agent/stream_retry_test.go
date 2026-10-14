package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

var errHTTP2Reset = errors.New("sse: stream error: stream ID 49; NO_ERROR; received from peer")

func fastStreamRetry(t *testing.T) {
	t.Helper()
	saved := streamRetryDelays
	streamRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { streamRetryDelays = saved })
}

func TestStreamRetryResendsAfterEarlyConnectionDrop(t *testing.T) {
	fastStreamRetry(t)
	p := &stubProvider{name: "test", scripts: [][]llm.Delta{
		{{Reasoning: "thinking..."}, {Err: errHTTP2Reset}},
		{{Content: "ok"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 1, Total: 6}}},
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "system")
	ch, err := l.Run(context.Background(), "hej")
	if err != nil {
		t.Fatal(err)
	}
	notice, done := false, false
	for _, ev := range drainEvents(t, ch) {
		switch e := ev.(type) {
		case NoticeEvent:
			notice = notice || strings.Contains(e.Text, "retrying")
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Fatalf("run failed: %v", e.Err)
		}
	}
	if p.calls != 2 || !notice || !done {
		t.Fatalf("calls=%d notice=%v done=%v", p.calls, notice, done)
	}
}

func TestStreamRetrySkipsWhenAnswerAlreadyVisible(t *testing.T) {
	fastStreamRetry(t)
	p := &stubProvider{name: "test", scripts: [][]llm.Delta{
		{{Content: "half a sentence"}, {Err: errHTTP2Reset}},
		{{Content: "should not run"}, {FinishReason: "stop"}},
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "system")
	ch, err := l.Run(context.Background(), "hej")
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, ev := range drainEvents(t, ch) {
		if _, ok := ev.(ErrorEvent); ok {
			failed = true
		}
	}
	if p.calls != 1 || !failed {
		t.Fatalf("calls=%d failed=%v; visible output must not be retried", p.calls, failed)
	}
}

func TestStreamRetryIgnoresPermanentErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("http 401: unauthorized"),
		errors.New("http 400: context length exceeded"),
		context.Canceled,
	} {
		if isTransientStreamError(err) {
			t.Errorf("%v must not be retried", err)
		}
	}
	if !isTransientStreamError(errHTTP2Reset) || !isTransientStreamError(errors.New("http: unexpected EOF")) {
		t.Fatal("transport drops must be retried")
	}
}
