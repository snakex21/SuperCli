package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

func TestVerifyConnectionForResponsesUsesResponsesWire(t *testing.T) {
	called := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["prompt_cache_key"] == "" || body["reasoning"] == nil {
			t.Fatalf("responses compatibility fields missing: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"OK"}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
		fmt.Fprintln(w)
	}))
	defer srv.Close()

	if err := VerifyConnectionForProvider(context.Background(), config.ProviderResponses, srv.URL, "test-key", "gpt-5.6-sol"); err != nil {
		t.Fatalf("VerifyConnectionForProvider: %v", err)
	}
	if called != 1 {
		t.Fatalf("calls = %d, want 1", called)
	}
}

func TestHumanizeVerifyErrorRecognizesResponsesHTTPFormat(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`http 401: {"error":"bad key"}`, "API key was rejected"},
		{`http 404: {"error":"missing endpoint"}`, "endpoint not found"},
		{`http 429: {"error":"slow down"}`, "rate limited"},
	}
	for _, tc := range tests {
		if got := humanizeVerifyError(tc.raw, "https://example.test/v1", "key").Error(); !strings.Contains(got, tc.want) {
			t.Errorf("humanizeVerifyError(%q) = %q, want substring %q", tc.raw, got, tc.want)
		}
	}
}
