package llm

// Audit 130: provider error text and the API key.
//
// The audit reproduced that a provider error body which echoes the bearer
// token (some gateways do: "Incorrect API key provided: <key>") is shown
// verbatim (HTTPResponseError.Error formats the raw response body). The
// project owner rejected redacting keys in errors, sessions.db or caches:
// provider errors are surfaced verbatim so the user sees exactly what the
// provider said. These tests document that behaviour and keep the one
// structural guarantee that is wanted: the key travels only in the
// Authorization header, never in the request URL.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sec130FakeKey = "sk-test-AUDIT130-SECRET-0123456789"

func sec130ProviderError(t *testing.T, wire a130Wire, status int) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s. Header: %s","type":"invalid_request_error"}}`,
			strings.TrimPrefix(auth, "Bearer "), auth)
	}))
	t.Cleanup(srv.Close)
	var (
		p   Provider
		err error
	)
	if wire == wireChat {
		p, err = NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", APIKey: sec130FakeKey, Model: "mock-model", Timeout: 5 * time.Second})
	} else {
		p, err = NewResponses(ResponsesConfig{BaseURL: srv.URL + "/v1", APIKey: sec130FakeKey, Model: "mock-model", Timeout: 5 * time.Second})
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		return err
	}
	for d := range ch {
		if d.Err != nil {
			return d.Err
		}
	}
	return errors.New("no error returned")
}

// Chosen behaviour: a permanent provider error is surfaced verbatim and
// typed (status kept for routing). Nothing is redacted — including a key the
// provider itself echoed back.
func TestAudit130ProviderErrorIsSurfacedVerbatim(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			err := sec130ProviderError(t, wire, http.StatusUnauthorized)
			var responseErr *HTTPResponseError
			if !errors.As(err, &responseErr) || responseErr.Status != http.StatusUnauthorized {
				t.Fatalf("401 not typed: %T %v", err, err)
			}
			if !strings.HasPrefix(err.Error(), "http 401: ") || !strings.Contains(err.Error(), "Incorrect API key provided: "+sec130FakeKey) {
				t.Fatalf("provider error not surfaced verbatim: %.200s", err.Error())
			}
			if ClassifyProviderError(err) != RetryPermanent {
				t.Fatalf("401 must not be retried: %s", ClassifyProviderError(err))
			}
		})
	}
}

// The request itself must carry the key only in the Authorization header,
// never in the URL (which would end up in proxies and error strings).
func TestAudit130APIKeyNotInRequestURL(t *testing.T) {
	var seenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad"}}`)
	}))
	defer srv.Close()
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", APIKey: sec130FakeKey, Model: "mock-model", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil {
		for range ch {
		}
	}
	if strings.Contains(seenURL, sec130FakeKey) {
		t.Fatalf("API key in request URL %q", seenURL)
	}
}
