package llm

// Audit 130: provider error text must not carry the configured API key.
//
// Reproduced end to end with the release binary (batch, --debug,
// --trace-jsonl) against a mock OpenAI endpoint that echoes the bearer token
// in a 401 / 500 body: the key is printed verbatim on stderr
// ("Error: http 401: {...Incorrect API key provided: <key>...}" and
// "[notice] connection to the model dropped (http 500: ... Authorization:
// Bearer <key> ...)"). The isolated data dir (logs, tool_errors.log,
// sessions.db, supercli.db) stayed clean. Responsible code:
// HTTPResponseError.Error (net_retry.go) formats the raw response body.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const sec130FakeKey = "sk-test-AUDIT130-SECRET-0123456789"

func sec130ProviderError(t *testing.T, status int) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s. Header: %s","type":"invalid_request_error"}}`,
			strings.TrimPrefix(auth, "Bearer "), auth)
	}))
	t.Cleanup(srv.Close)
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", APIKey: sec130FakeKey, Model: "mock-model", Timeout: 5 * time.Second})
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

func TestAudit130ProviderErrorDoesNotEchoAPIKey(t *testing.T) {
	if os.Getenv("SUPERCLI_AUDIT_KNOWN_FAILING") != "1" {
		t.Skip("KNOWN-FAILING audit-130: provider error bodies that echo the API key are surfaced verbatim (HTTPResponseError.Error)")
	}
	err := sec130ProviderError(t, http.StatusUnauthorized)
	if strings.Contains(err.Error(), sec130FakeKey) {
		t.Errorf("401 error text contains the configured API key: %.120s...", err.Error())
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
