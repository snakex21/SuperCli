package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
)

// streamRetryDelays are the waits before re-sending a model request whose
// connection broke before any visible answer or tool call arrived (e.g. an
// HTTP/2 "stream error: stream ID 49; NO_ERROR; received from peer").
var streamRetryDelays = []time.Duration{time.Second, 3 * time.Second}

// transientStreamErrors are transport failures where the same request can
// succeed when sent again. Provider HTTP status errors are retried inside
// the providers; context-limit and auth errors never match this list.
var transientStreamErrors = []string{
	"stream error: stream id",
	"http2: server sent goaway",
	"http2: client connection lost",
	"http2: response body closed",
	"unexpected eof",
	"connection reset by peer",
	"forcibly closed by the remote host",
	"broken pipe",
	"sse: empty stream",
	"stream ended before",
}

func isTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range transientStreamErrors {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// nothingVisibleYet reports whether a failed attempt delivered no answer
// text and no tool call, so a retry cannot duplicate output. Reasoning that
// was streamed before the break does not count.
func nothingVisibleYet(text string, calls []llm.ToolCall) bool {
	if len(calls) > 0 {
		return false
	}
	plain, _ := captureThinking(text)
	return strings.TrimSpace(plain) == ""
}

// completeWithStreamRetry runs completeOnce and re-sends the request when
// the connection broke before anything visible arrived.
func (l *Loop) completeWithStreamRetry(ctx context.Context, toolRequest preparedToolRequest, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	text, calls, usage, err := l.completeOnce(ctx, toolRequest, out)
	for attempt := 0; attempt < len(streamRetryDelays); attempt++ {
		if err == nil || ctx.Err() != nil || !isTransientStreamError(err) || !nothingVisibleYet(text, calls) {
			break
		}
		wait := streamRetryDelays[attempt]
		select {
		case out <- NoticeEvent{Text: fmt.Sprintf("connection to the model dropped (%v) — retrying in %s (attempt %d/%d)",
			err, wait, attempt+2, len(streamRetryDelays)+1)}:
		case <-ctx.Done():
			return text, calls, usage, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return text, calls, usage, err
		}
		text, calls, usage, err = l.completeOnce(ctx, toolRequest, out)
	}
	return text, calls, usage, err
}
