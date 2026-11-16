package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// ErrStreamIdleTimeout is the errors.Is target of every StreamIdleTimeoutError.
var ErrStreamIdleTimeout = errors.New("llm: provider stream idle timeout")

// StreamIdleTimeoutError reports that the provider sent nothing for Idle:
// either no response headers (Headers) or no SSE bytes on an accepted stream.
// The idle watchdog cancels the request to free the connection, but the error
// is deliberately NOT context.Canceled: a stall is a provider/network fault,
// classified transient so the bounded agent retry can ask again, while a user
// cancel stays permanent.
type StreamIdleTimeoutError struct {
	Idle    time.Duration
	Headers bool
}

func (e *StreamIdleTimeoutError) Error() string {
	if e.Headers {
		// Historical text; users and tests match "response headers timeout".
		return fmt.Sprintf("response headers timeout after %s", e.Idle)
	}
	return fmt.Sprintf("no data from provider for %s (idle timeout)", e.Idle)
}

func (e *StreamIdleTimeoutError) Is(target error) bool { return target == ErrStreamIdleTimeout }

// StreamTransportError is a dropped connection (reset, closed, unexpected EOF,
// HTTP/2 GOAWAY, network timeout) seen while sending the request or reading an
// accepted stream. Op keeps the historical text prefix ("http" / "sse"), so
// Error() is unchanged; the type only adds the transient classification.
type StreamTransportError struct {
	Op  string
	Err error
}

func (e *StreamTransportError) Error() string {
	if e.Op == "" {
		return e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *StreamTransportError) Unwrap() error { return e.Err }

// IsStreamTransportFailure reports whether err is a dropped connection or an
// idle/header timeout of a provider stream (both classified transient).
func IsStreamTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	var transport *StreamTransportError
	return errors.As(err, &transport) || errors.Is(err, ErrStreamIdleTimeout)
}

// wrapStreamError gives a transport read/send failure its typed class and the
// historical "op: cause" text. Idle timeouts keep their own type; user
// cancellation, provider error events and parse errors stay as they were.
func wrapStreamError(op string, err error) error {
	if err == nil {
		return nil
	}
	var idle *StreamIdleTimeoutError
	if errors.As(err, &idle) {
		return fmt.Errorf("%s: %w", op, err)
	}
	if isTransportDropError(err) {
		return &StreamTransportError{Op: op, Err: err}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isTransportDropError recognizes failures where the same request can succeed
// when sent again: the peer reset or closed the connection, the body ended
// mid-message, or an HTTP/2 stream was torn down. DNS misses, TLS failures,
// refused connections and cancellation are not drops.
func isTransportDropError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Provider-reported errors and payload problems keep their own class.
	var providerErr *ProviderStreamError
	var responseErr *HTTPResponseError
	var runaway *RunawayToolCallError
	if errors.As(err, &providerErr) || errors.As(err, &responseErr) || errors.As(err, &runaway) || errors.Is(err, errSSEUnexpected) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, code := range []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE,
		syscall.ENETDOWN, syscall.ENETUNREACH, syscall.EHOSTUNREACH} {
		if errors.Is(err, code) {
			return true
		}
	}
	if runtime.GOOS == "windows" {
		// WSAENETDOWN, WSAENETUNREACH, WSAENETRESET, WSAECONNABORTED,
		// WSAECONNRESET, WSAETIMEDOUT, WSAEHOSTUNREACH.
		for _, code := range []syscall.Errno{10050, 10051, 10052, 10053, 10054, 10060, 10065} {
			if errors.Is(err, code) {
				return true
			}
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"unexpected eof",
		"connection reset",
		"forcibly closed by the remote host",
		"broken pipe",
		"stream error: stream id",
		"http2: server sent goaway",
		"http2: client connection lost",
		"http2: response body closed",
		"server closed idle connection",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
