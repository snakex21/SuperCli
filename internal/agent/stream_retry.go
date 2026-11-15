package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"runtime"
	"strings"
	"syscall"
	"time"

	"supercli/internal/llm"
)

// Retry seams keep outage recovery tests deterministic and socket-free. The
// count comes from the shared llm retry setting (config.toml max_retries,
// -1 = until success or cancel); the wait is llm.RetryBackoff (exponential
// from 1 s, jittered, capped at 60 s) and always selects on ctx.Done().
var streamRetryWait = llm.SleepRetry
var streamRetryBackoff = func(retry int) time.Duration { return llm.RetryBackoff(retry, time.Second) }

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

// providerCompleteError records the synchronous Complete boundary separately
// from errors delivered on an accepted stream. Preserve both the historical
// display text and the original cause for errors.Is/As callers.
type providerCompleteError struct {
	cause          error
	streamReturned bool
}

func (e *providerCompleteError) Error() string { return "agent: provider.Complete: " + e.cause.Error() }
func (e *providerCompleteError) Unwrap() error { return e.cause }

// acceptedStreamError blocks generic transport replay of accepted streams.
// hadProgress also blocks context-limit repair after any model progress, even
// undispatched start signals. Only a context rejection with no progress keeps
// the established explicit compaction path; this is not native stream resume.
// Leave truncatedToolResponseError unwrapped: it has a separate repair path.
type acceptedStreamError struct {
	cause       error
	hadProgress bool
}

func (e *acceptedStreamError) Error() string { return e.cause.Error() }
func (e *acceptedStreamError) Unwrap() error { return e.cause }

// Walk both standard unwrap shapes, carrying transport provenance down each
// branch. A URL/OpError in one Join branch must not qualify its siblings.
func walkStartupErrorTree(err error, transport bool, match func(error, bool) bool) bool {
	if err == nil {
		return false
	}
	switch err.(type) {
	case *url.Error, *net.OpError:
		transport = true
	}
	if match(err, transport) {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if walkStartupErrorTree(child, transport, match) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return walkStartupErrorTree(wrapped.Unwrap(), transport, match)
	}
	return false
}

func hasTransientTransportError(err error) bool {
	return walkStartupErrorTree(err, false, func(node error, transport bool) bool {
		network, ok := node.(net.Error)
		return transport && ok && (network.Timeout() || network.Temporary())
	})
}

func startupTransportCause(err error) (error, bool) {
	var accepted *acceptedStreamError
	if errors.As(err, &accepted) {
		return nil, false
	}
	var startup *providerCompleteError
	if !errors.As(err, &startup) || startup.streamReturned || errors.Is(startup.cause, context.Canceled) {
		return nil, false
	}
	var response *llm.HTTPResponseError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	cause := startup.cause
	// Inspect every branch: the first DNS match can be temporary while a
	// later joined error is permanent. Any permanent DNS veto wins.
	permanentDNS := walkStartupErrorTree(cause, false, func(node error, _ bool) bool {
		var dns *net.DNSError
		return errors.As(node, &dns) && (dns.IsNotFound || (!dns.Timeout() && !dns.Temporary()))
	})
	if errors.As(cause, &response) || errors.As(cause, &certificate) || errors.As(cause, &record) ||
		errors.As(cause, &alert) || errors.As(cause, &authority) || errors.As(cause, &invalid) || errors.As(cause, &hostname) ||
		permanentDNS {
		return nil, false
	}
	return cause, true
}

func isTransientCompletionError(err error) bool {
	cause, ok := startupTransportCause(err)
	if !ok {
		return false
	}
	// Only typed transport failures qualify here, never an auth/configuration
	// message that happens to contain a streamed-error marker. Caller deadlines
	// are checked by the retry loop; a live caller may retry a client's shorter
	// timeout (url.Error/net.OpError), but not a bare context deadline error.
	if errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF) || hasTransientTransportError(cause) {
		return true
	}
	var op *net.OpError
	if errors.As(cause, &op) {
		return errors.Is(op, syscall.ECONNRESET) ||
			errors.Is(op, syscall.ECONNABORTED) || errors.Is(op, syscall.EPIPE) || isStartupHTTP2TransportError(op.Err)
	}
	var request *url.Error
	return errors.As(cause, &request) && isStartupHTTP2TransportError(request.Err)
}

// HTTP/2 stream/GOAWAY errors can lack net.Error. Retain the existing HTTP/2
// markers only on the transport cause inside a typed startup url/OpError, not
// on arbitrary startup text, HTTP response bodies, or streamed SSE errors.
func isStartupHTTP2TransportError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"stream error: stream id",
		"http2: server sent goaway",
		"http2: client connection lost",
		"http2: response body closed",
	} {
		if strings.HasPrefix(text, marker) {
			return true
		}
	}
	return false
}

// nothingVisibleYet is an additional guard, not permission to retry reasoning.
// Only synchronous startup failures without a returned stream are replayable.
func nothingVisibleYet(text string, calls []llm.ToolCall) bool {
	if len(calls) > 0 {
		return false
	}
	plain, _ := captureThinking(text)
	return strings.TrimSpace(plain) == ""
}

// Typed connectivity failures (network down, unreachable host, reset) get a
// quieter "waiting" notice; EOF/HTTP2 compatibility failures report the drop.
// HTTP status retries remain entirely provider-owned.
func isShortNetworkOutage(err error) bool {
	cause, ok := startupTransportCause(err)
	if !ok {
		return false
	}
	if hasTransientTransportError(cause) {
		return true
	}
	return walkStartupErrorTree(cause, false, func(node error, transport bool) bool {
		return transport && isConnectivityErrno(node)
	})
}

func isConnectivityErrno(err error) bool {
	for _, code := range []syscall.Errno{syscall.ENETDOWN, syscall.ENETUNREACH, syscall.EHOSTUNREACH,
		syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE} {
		if errors.Is(err, code) {
			return true
		}
	}
	// Windows socket errors use Winsock numbers, not Go's POSIX errno values.
	// WSAENETDOWN, WSAENETUNREACH, WSAENETRESET, WSAECONNABORTED,
	// WSAECONNRESET, WSAETIMEDOUT, WSAEHOSTUNREACH, respectively.
	if runtime.GOOS == "windows" {
		for _, code := range []syscall.Errno{10050, 10051, 10052, 10053, 10054, 10060, 10065} {
			if errors.Is(err, code) {
				return true
			}
		}
	}
	return false
}

// retryableAcceptedStreamError reports whether an error delivered INSIDE an
// accepted stream may be re-requested: a provider error event classified as
// transient (rate limit, overload, temporarily unavailable, capacity — e.g.
// Cloudflare Workers AI "rate limiting: inference request per min rate
// reached"), or an unclassified one when retry_unknown_errors is on. Context
// overflow, auth, invalid-request and quota errors never qualify. The caller
// additionally requires that no tool call and no visible answer text was
// produced, so nothing was executed and nothing shown is duplicated.
func retryableAcceptedStreamError(err error) bool {
	var accepted *acceptedStreamError
	if !errors.As(err, &accepted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isContextLimitErr(err) {
		return false
	}
	var startup *providerCompleteError
	if errors.As(err, &startup) {
		return false
	}
	return llm.ShouldRetryProviderError(accepted.cause)
}

// Retry only the failed model call, never its enclosing step/tool batch:
// pre-stream transport failures, and accepted streams that failed with a
// retryable provider error before any tool call or visible answer text. The
// number of retries is the shared max_retries setting (unlimited when -1);
// every wait ends immediately when ctx is cancelled (Ctrl+C, Esc, Stop, the
// batch signal).
func (l *Loop) completeWithStreamRetry(ctx context.Context, toolRequest preparedToolRequest, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	text, calls, usage, err := l.completeOnce(ctx, toolRequest, out)
	for retries := 0; ; {
		if ctx.Err() != nil {
			return text, calls, usage, ctx.Err()
		}
		if err == nil || !nothingVisibleYet(text, calls) {
			break
		}
		outage := isShortNetworkOutage(err)
		inStream := !outage && retryableAcceptedStreamError(err)
		if !outage && !inStream && !isTransientCompletionError(err) {
			break
		}
		if !llm.RetryAllowed(retries) {
			break
		}
		retries++
		wait := streamRetryBackoff(retries)
		progress := llm.RetryProgress(retries)
		var notice string
		switch {
		case outage:
			notice = fmt.Sprintf("Model connection unavailable — waiting %s before retry %s.", wait.Round(100*time.Millisecond), progress)
		default:
			notice = fmt.Sprintf("connection to the model dropped (%v) — retrying in %s (retry %s)", err, wait.Round(100*time.Millisecond), progress)
		}
		select {
		case out <- NoticeEvent{Text: notice}:
		case <-ctx.Done():
			return text, calls, usage, ctx.Err()
		}
		if waitErr := streamRetryWait(ctx, wait); waitErr != nil {
			return text, calls, usage, waitErr
		}
		if ctx.Err() != nil {
			return text, calls, usage, ctx.Err()
		}
		text, calls, usage, err = l.completeOnce(llm.WithCallReason(ctx, llm.ReasonTransportRetry), toolRequest, out)
	}
	return text, calls, usage, err
}
