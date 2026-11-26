package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"supercli/internal/llm"
)

// Surviving a network change (after 1.0.41). Switching from cable to Wi-Fi
// killed running work: a model call failing with a reset, EOF mid-stream,
// "wsarecv", "no such host" or "network is unreachable" got the ordinary
// retry budget (5 retries, about 30 s in all), a DNS "no such host" none at
// all, and a stream that broke after visible output none either. The turn
// or worker then failed.
//
// A model call that fails because the network is gone now waits for it (a
// cheap connectivity probe, backoff up to 15 s) for llm.NetworkWait (10 min
// by default), and then re-sends the whole request. Output the broken stream
// already showed is withdrawn (StreamDiscardEvent) so nothing is shown or
// saved twice, and its unfinished tool calls are never executed. The UI shows
// "no connection — waiting for the network (0:42)" (NetworkWaitEvent). Only
// if the network stays away is the run stopped, as a resumable network stop.
// Provider HTTP errors (4xx, limits) are never treated as network errors.

// NetworkWaitEvent: the model call waits for the network (Waiting) or the
// connection is back (Waiting false). Since is when the wait began.
type NetworkWaitEvent struct {
	Waiting bool
	Since   time.Time
}

func (NetworkWaitEvent) event() {}

// StreamDiscardEvent withdraws the output a broken stream already showed: it
// is sent again by the retried request. Text is the visible answer part,
// Reasoning the reasoning part.
type StreamDiscardEvent struct {
	Text      string
	Reasoning string
}

func (StreamDiscardEvent) event() {}

// NetworkStopError ends a run after the network stayed away for the whole
// wait. The run can be resumed; nothing was lost.
type NetworkStopError struct {
	Waited time.Duration
	Cause  error
}

func (e *NetworkStopError) Error() string {
	return fmt.Sprintf("network stop: no connection to the model for %s (%v); the work is kept and can be resumed", e.Waited.Round(time.Second), e.Cause)
}

func (e *NetworkStopError) Unwrap() error { return e.Cause }

// IsNetworkStop reports a run stopped because the network stayed away.
func IsNetworkStop(err error) bool {
	var stop *NetworkStopError
	return errors.As(err, &stop)
}

// networkDropMarkers are transport failures seen when the connection goes
// away (any provider, any OS), matched on the error text when the typed
// error was lost on the way.
var networkDropMarkers = []string{
	"connection reset", "forcibly closed", "connection aborted", "broken pipe",
	"unexpected eof", "wsarecv", "wsasend", "no such host", "network is unreachable",
	"network is down", "host is unreachable", "no route to host", "tls handshake timeout",
	"i/o timeout", "http2: client connection lost", "http2: server sent goaway",
	"server misbehaving", "temporary failure in name resolution",
	"no data from provider", "no response from provider",
}

// isNetworkDrop classifies a failed model call as "the network went away".
// HTTP responses (4xx, 5xx, limits), certificate problems, cancellation and
// context-limit errors are never network drops.
func isNetworkDrop(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var response *llm.HTTPResponseError
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &response) || errors.As(err, &certificate) || errors.As(err, &authority) || errors.As(err, &hostname) || isContextLimitErr(err) {
		return false
	}
	if isShortNetworkOutage(err) || isTransientCompletionError(err) || llm.IsStreamTransportFailure(err) {
		return true
	}
	// A refused connection is a server that is down, not a lost network.
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(strings.ToLower(err.Error()), "connection refused") ||
		strings.Contains(strings.ToLower(err.Error()), "actively refused") {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false // a caller's deadline, not the network
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "http ") && strings.Contains(text, ": {") {
		return false // an HTTP error body that mentions a network word
	}
	for _, marker := range networkDropMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// networkProbe reports whether the network looks usable again: an interface
// with a routable address and, when the provider's host is known, a name
// lookup of it. Cheap; the real check is the retried request. A seam for tests.
var networkProbe = func(ctx context.Context, host string) bool {
	if !hasRoutableInterface() {
		return false
	}
	if host == "" {
		return true
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := net.DefaultResolver.LookupHost(probeCtx, host)
	return err == nil
}

func hasRoutableInterface() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true // unknown: let the request decide
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
				return true
			}
		}
	}
	return false
}

// networkUpRetries: calls re-sent while the network already looks up.
const networkUpRetries = 4

// networkClock is the wait's clock (a seam for tests).
var networkClock = time.Now

// networkProbeBackoff is the pause before the n-th connectivity probe.
func networkProbeBackoff(n int) time.Duration {
	d := time.Second << min(n, 4) // 1, 2, 4, 8, 16 s
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

// providerHost is the host name of the provider's endpoint when it says.
func (l *Loop) providerHost() string {
	if p, ok := l.provider.(interface{ BaseURL() string }); ok {
		raw := p.BaseURL()
		if i := strings.Index(raw, "://"); i >= 0 {
			raw = raw[i+3:]
		}
		raw, _, _ = strings.Cut(raw, "/")
		if h, _, err := net.SplitHostPort(raw); err == nil {
			return h
		}
		return raw
	}
	return ""
}

// retryThroughNetworkLoss re-sends a model call that failed because the
// network went away, once the network is back, for up to llm.NetworkWait.
// failed* are the broken attempt's results.
func (l *Loop) retryThroughNetworkLoss(ctx context.Context, toolRequest preparedToolRequest, out chan<- Event,
	text string, calls []llm.ToolCall, usage *llm.Usage, err error) (string, []llm.ToolCall, *llm.Usage, error) {
	started := networkClock()
	limit := llm.NetworkWait()
	host := l.providerHost()
	send := func(ev Event) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	waiting := false
	// The network looks up (the probe passes at once) but the call still
	// fails: after a few tries it is not a lost network; the ordinary error
	// handling takes over.
	upFailures := 0
	defer func() {
		if waiting {
			// Cleared on success and on a final stop alike.
			out <- NetworkWaitEvent{Waiting: false, Since: started}
		}
	}()
	// A failure before any output while the network still looks up is left
	// to the ordinary retry ladder (a brief blip or a server problem); this
	// wait is for a network that is really gone, or a stream that broke after
	// output had been shown (which the ladder cannot replay).
	if nothingVisibleYet(text, calls) && networkProbe(ctx, host) {
		return text, calls, usage, err
	}
	for probes := 0; isNetworkDrop(err); {
		if ctx.Err() != nil {
			return text, calls, usage, ctx.Err()
		}
		// Decide first; a broken reply is withdrawn only right before it is
		// sent again, so a final stop keeps what was shown and saved alike.
		if networkProbe(ctx, host) {
			// The network looks up: a short pause, a few times at most.
			upFailures++
			if upFailures > networkUpRetries {
				return text, calls, usage, err
			}
			if waitErr := streamRetryWait(ctx, time.Duration(upFailures)*500*time.Millisecond); waitErr != nil {
				return text, calls, usage, waitErr
			}
		} else {
			upFailures = 0
			if !waiting {
				waiting = true
				if !send(NetworkWaitEvent{Waiting: true, Since: started}) {
					return text, calls, usage, ctx.Err()
				}
			}
			for {
				if networkClock().Sub(started) >= limit {
					return text, calls, usage, &NetworkStopError{Waited: networkClock().Sub(started), Cause: err}
				}
				if waitErr := streamRetryWait(ctx, networkProbeBackoff(probes)); waitErr != nil {
					return text, calls, usage, waitErr
				}
				probes++
				if networkProbe(ctx, host) {
					break
				}
			}
		}
		// Withdraw what the broken stream showed; its calls never ran.
		if plain, reasoning := captureThinking(text); strings.TrimSpace(plain) != "" || strings.TrimSpace(reasoning) != "" {
			if !send(StreamDiscardEvent{Text: plain, Reasoning: reasoning}) {
				return text, calls, usage, ctx.Err()
			}
		}
		l.noteDiscardedUsage(usage)
		text, calls, usage, err = l.completeOnce(llm.WithCallReason(ctx, llm.ReasonTransportRetry), toolRequest, out)
	}
	return text, calls, usage, err
}
