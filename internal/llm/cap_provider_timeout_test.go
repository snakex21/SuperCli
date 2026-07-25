package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
)

// A timeout means "we stopped waiting", a refusal means "the server said no".
// Provider setup treats the two differently, so the classifier must not blur
// them: calling a rejected key "slow" would let bad credentials through.
func TestIsTimeoutError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context deadline", context.DeadlineExceeded, true},
		{"wrapped context deadline", fmt.Errorf("llm: ListProviderModels: %w", context.DeadlineExceeded), true},
		{"os deadline", os.ErrDeadlineExceeded, true},
		{"url error wrapping deadline", &url.Error{
			Op: "Get", URL: "https://example.invalid/v1/models", Err: context.DeadlineExceeded,
		}, true},
		{"net error that timed out", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, true},
		{"http client timeout text", errors.New(`Get "https://x/v1/models": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), true},

		{"rejected key", errors.New("llm: ListProviderModels: status 401: invalid API key"), false},
		{"not found", errors.New("llm: ListProviderModels: status 404: no such endpoint"), false},
		{"connection refused", errors.New(`Get "http://127.0.0.1:1/v1/models": dial tcp 127.0.0.1:1: connectex: connection refused`), false},
		{"no such host", &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"parse failure", errors.New("llm: ListProviderModels: parse: unexpected end of JSON input"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTimeoutError(tc.err); got != tc.want {
				t.Fatalf("IsTimeoutError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The discovery budget must be at least as large as the slowest wrapper that
// depends on it; a caller context shorter than the HTTP client's own timeout
// was exactly the bug that made slow gateways unaddable.
func TestProviderDiscoveryTimeoutCoversSlowGateways(t *testing.T) {
	// Measured worst case on a real queueing gateway was ~27s.
	if ProviderDiscoveryTimeout < 30_000_000_000 {
		t.Fatalf("ProviderDiscoveryTimeout = %s, too short for a queueing gateway", ProviderDiscoveryTimeout)
	}
}
