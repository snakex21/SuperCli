package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anyrouterModels is the live inventory the reseller's /v1/models returned on
// 2026-07-25. gpt-5.6-sol is in it and is nevertheless rejected — the catalog
// is not stale, the gateway simply stopped routing the model. That is why the
// hint prints the list rather than only telling the user to rescan.
var anyrouterModels = []string{
	"claude-3-5-haiku-20241022", "claude-3-5-sonnet-20241022",
	"claude-3-7-sonnet-20250219", "claude-fable-5",
	"claude-haiku-4-5-20251001", "claude-opus-4-1-20250805",
	"claude-opus-4-20250514", "claude-opus-4-5-20251101",
	"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8",
	"claude-sonnet-4-20250514", "claude-sonnet-4-5-20250929",
	"gemini-2.5-pro", "gpt-5-codex", "gpt-5.6-sol",
}

func withCatalog(t *testing.T, baseURL string, models []string) {
	t.Helper()
	clearProviderModelCatalog()
	t.Cleanup(clearProviderModelCatalog)
	RememberProviderModels(baseURL, models)
}

// TestModelUnavailableHintAnyrouter404 is the reported failure verbatim: a
// Chinese-language 404 whose only content is the rejected model id.
func TestModelUnavailableHintAnyrouter404(t *testing.T) {
	const base = "https://anyrouter.top/v1"
	withCatalog(t, base, anyrouterModels)

	body := []byte(`{"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}`)
	hint := providerErrorHint(base, "gpt-5.6-sol", 404, body)
	if hint == "" {
		t.Fatal("no hint for a provider rejecting the configured model")
	}
	if !strings.Contains(hint, `model "gpt-5.6-sol" is not available at this provider`) {
		t.Errorf("hint does not name the problem: %s", hint)
	}
	// The endpoint's other gpt-* model must survive truncation — it is the
	// closest thing to what the user asked for.
	if !strings.Contains(hint, "gpt-5-codex") {
		t.Errorf("hint drops the same-family model: %s", hint)
	}
	for _, want := range []string{"claude-fable-5", "claude-3-5-haiku-20241022"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint omits available model %q: %s", want, hint)
		}
	}
	if strings.Contains(hint, "gpt-5.6-sol,") || strings.Contains(hint, ", gpt-5.6-sol") {
		t.Errorf("hint offers the rejected model as an alternative: %s", hint)
	}
	if !strings.Contains(hint, "and 5 more") {
		t.Errorf("hint should truncate a 15-model list to %d + a count: %s",
			modelUnavailableListLimit, hint)
	}
}

// TestModelUnavailableHintStatusVariants covers the codes providers actually
// use for this class. anyrouter answers 404 for a model it never routed and
// 400 for one it retired ("claude-opus-4-6 已下线, 请切换到 claude-opus-4-7"),
// so the status code alone can never be the test.
func TestModelUnavailableHintStatusVariants(t *testing.T) {
	const base = "https://gateway.test/v1"
	cases := []struct {
		name   string
		status int
		model  string
		body   string
	}{
		{"404 chinese", 404, "gpt-5.6-sol", `{"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}`},
		{"400 chinese retired", 400, "claude-opus-4-6", `{"error":"claude-opus-4-6 已下线，请切换到 claude-opus-4-7 模型","type":"error"}`},
		{"400 english", 400, "gpt-5.6-sol", `{"error":{"message":"The model gpt-5.6-sol does not exist","type":"invalid_request_error"}}`},
		{"400 wrapped upstream unavailable", 400, "gpt-5.6-sol", `{"error":{"type":"server_error","message":"Error from provider (Console): Upstream request failed: Model is unavailable."}}`},
		{"422 english", 422, "gpt-5.6-sol", `{"detail":"unknown_model"}`},
		{"404 code only", 404, "gpt-5.6-sol", `{"error":{"code":"model_not_found","message":"unavailable"}}`},
		{"200 with error body", 200, "gpt-5.6-sol", `{"error":"当前 API 不支持所选模型 gpt-5.6-sol"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCatalog(t, base, []string{"claude-opus-4-7", "gpt-5.6"})
			hint := modelUnavailableHint(base, tc.model, tc.status, []byte(tc.body))
			// A 200 is not an error path any transport reaches here; it is
			// listed to document that the classifier stays scoped to 4xx.
			if tc.status == 200 {
				if hint != "" {
					t.Fatalf("2xx must not be classified as a model error: %s", hint)
				}
				return
			}
			if hint == "" {
				t.Fatalf("http %d not recognised as model-unavailable: %s", tc.status, tc.body)
			}
			if !strings.Contains(hint, "available: ") {
				t.Errorf("hint carries no alternatives: %s", hint)
			}
		})
	}
}

// TestModelUnavailableHintNotAWrongURL404 is the regression that matters most:
// a plain wrong-path 404 must stay a wrong-path 404.
func TestModelUnavailableHintNotAWrongURL404(t *testing.T) {
	const base = "https://api.openai.test/v1"
	withCatalog(t, base, []string{"gpt-5.6", "claude-opus-4-7"})
	bodies := []string{
		`{"error":{"message":"Invalid URL (POST /v1/chat/completion)","type":"invalid_request_error","code":null}}`,
		`<html><head><title>404 Not Found</title></head><body>nginx</body></html>`,
		`{"error":{"message":"Not Found"}}`,
	}
	for _, body := range bodies {
		if hint := modelUnavailableHint(base, "gpt-5.6-sol", 404, []byte(body)); hint != "" {
			t.Errorf("wrong-path 404 misread as a model error\n body: %s\n hint: %s", body, hint)
		}
	}
}

// TestModelUnavailableHintNotAnAuthError keeps a bad key reported as a bad key.
func TestModelUnavailableHintNotAnAuthError(t *testing.T) {
	const base = "https://anyrouter.top/v1"
	withCatalog(t, base, anyrouterModels)
	bodies := []string{
		`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`,
		// Worst case: the gateway quotes the model inside an auth failure.
		`{"error":"无效的令牌，模型 gpt-5.6-sol","type":"error"}`,
	}
	for _, status := range []int{401, 403} {
		for _, body := range bodies {
			if hint := providerErrorHint(base, "gpt-5.6-sol", status, []byte(body)); hint != "" {
				t.Errorf("http %d auth failure reported as a model problem: %s", status, hint)
			}
		}
	}
}

// TestProviderErrorHintKeepsRateLimitAndEffort checks the other two classes
// still win where they should.
func TestProviderErrorHintKeepsRateLimitAndEffort(t *testing.T) {
	const base = "https://gateway.test/v1"
	withCatalog(t, base, anyrouterModels)

	hint := providerErrorHint(base, "gpt-5.6-sol", 429, []byte(`{"error":"当前 API 不支持所选模型 gpt-5.6-sol"}`))
	if !strings.Contains(hint, "rate-limited") {
		t.Errorf("429 must stay a rate-limit hint, got: %s", hint)
	}
	// anyrouter answers 503/429 "Service Unavailable" when the upstream has no
	// capacity for an otherwise perfectly valid model. Capacity is not
	// availability, and the two must not be conflated.
	for _, status := range []int{500, 502, 503} {
		if h := modelUnavailableHint(base, "claude-opus-4-8", status,
			[]byte(`{"error":{"message":"Service Unavailable","type":"error"}}`)); h != "" {
			t.Errorf("http %d upstream outage reported as a missing model: %s", status, h)
		}
	}

	if err := SetReasoningEffort("xhigh"); err != nil {
		t.Fatalf("SetReasoningEffort: %v", err)
	}
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	effortBody := []byte(`{"error":{"message":"Unsupported value: 'xhigh' is not supported with model gpt-5.6-sol. Supported values are: 'low', 'medium', 'high'.","param":"reasoning_effort"}}`)
	hint = providerErrorHint(base, "gpt-5.6-sol", 400, effortBody)
	if strings.Contains(hint, "not available at this provider") {
		t.Errorf("a rejected reasoning parameter must not be read as a missing model: %s", hint)
	}
	if !strings.Contains(hint, "reasoning effort") {
		t.Errorf("effort hint lost: %s", hint)
	}
	// And without the effort hint in play, the parameter complaint is still
	// not a model complaint.
	_ = SetReasoningEffort("")
	if h := modelUnavailableHint(base, "gpt-5.6-sol", 400, effortBody); h != "" {
		t.Errorf("parameter rejection classified as model-unavailable: %s", h)
	}
}

func TestModelUnavailableHintWithoutCatalog(t *testing.T) {
	const base = "https://unscanned.test/v1"
	withCatalog(t, base, nil)
	hint := modelUnavailableHint(base, "gpt-5.6-sol", 404,
		[]byte(`{"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}`))
	if !strings.Contains(hint, "no model list is cached") {
		t.Errorf("an unknown inventory must say so, got: %s", hint)
	}
	if strings.Contains(hint, "available: ") {
		t.Errorf("hint invents a list it does not have: %s", hint)
	}
}

func TestNearestModel(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		available []string
		want      string
	}{
		{"suffix on a real id", "gpt-5.6-sol", []string{"gpt-5.6", "claude-opus-4-7"}, "gpt-5.6"},
		{"longest segment prefix wins", "gpt-5.6-sol", []string{"gpt-5", "gpt-5.6"}, "gpt-5.6"},
		{"typo", "claude-opus-4-8", []string{"claude-opus-4-7", "gemini-2.5-pro"}, "claude-opus-4-7"},
		{"ambiguous typo stays silent", "claude-opus-4-6", []string{"claude-opus-4-5", "claude-opus-4-7"}, ""},
		{"case-variant prefixes stay silent", "gpt-5.6-sol", []string{"GPT-5.6", "gpt-5.6"}, ""},
		{"unrelated name stays silent", "gpt-5.6-sol", []string{"claude-opus-4-7", "gemini-2.5-pro"}, ""},
		{"distant name stays silent", "gpt-5.6-sol", []string{"claude-opus-4-7-thinking"}, ""},
		{"no catalog stays silent", "gpt-5.6-sol", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nearestModel(tc.model, tc.available); got != tc.want {
				t.Errorf("nearestModel(%q, %v) = %q, want %q", tc.model, tc.available, got, tc.want)
			}
		})
	}
}

func TestIsSegmentPrefix(t *testing.T) {
	cases := []struct {
		prefix, s string
		want      bool
	}{
		{"gpt-5.6", "gpt-5.6-sol", true},
		{"gpt-5", "gpt-5.6-sol", true},
		{"gpt-5", "gpt-56", false}, // must stop on a separator, not mid-token
		{"gpt-5.6-sol", "gpt-5.6-sol", false},
		{"gpt-5.6-sol-x", "gpt-5.6-sol", false},
		{"", "gpt-5.6-sol", false},
	}
	for _, tc := range cases {
		if got := isSegmentPrefix(tc.prefix, tc.s); got != tc.want {
			t.Errorf("isSegmentPrefix(%q, %q) = %v, want %v", tc.prefix, tc.s, got, tc.want)
		}
	}
}

func TestNearestModelSurfacesInHint(t *testing.T) {
	const base = "https://gateway.test/v1"
	withCatalog(t, base, []string{"gpt-5.6", "claude-opus-4-7"})
	hint := modelUnavailableHint(base, "gpt-5.6-sol", 404,
		[]byte(`{"error":"当前 API 不支持所选模型 gpt-5.6-sol"}`))
	if !strings.Contains(hint, `did you mean "gpt-5.6"?`) {
		t.Errorf("hint should suggest the nearest real id: %s", hint)
	}
}

func TestRememberProviderModelsNormalizesKey(t *testing.T) {
	clearProviderModelCatalog()
	t.Cleanup(clearProviderModelCatalog)
	RememberProviderModels("https://AnyRouter.top/v1/", []string{" b ", "a", "a", ""})
	got := ProviderModels("https://anyrouter.top/v1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ProviderModels = %v, want [a b]", got)
	}
	RememberProviderModels("https://anyrouter.top/v1", nil)
	if got := ProviderModels("https://anyrouter.top/v1"); got != nil {
		t.Errorf("an empty scan must clear the entry, got %v", got)
	}
}

// TestOpenAIStreamSurfacesModelUnavailableHint drives the real transport: this
// is what the user sees in place of the bare `http 404: {"error":"当前 API ..."}`.
func TestOpenAIStreamSurfacesModelUnavailableHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}`)
	}))
	defer srv.Close()

	base := srv.URL + "/v1"
	withCatalog(t, base, anyrouterModels)

	p, err := NewOpenAI(OpenAIConfig{BaseURL: base, APIKey: "k", Model: "gpt-5.6-sol"})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var streamErr error
	for d := range ch {
		if d.Err != nil {
			streamErr = d.Err
		}
	}
	if streamErr == nil {
		t.Fatal("expected a terminal error")
	}
	msg := streamErr.Error()
	if !strings.Contains(msg, "http 404") {
		t.Errorf("status lost from the error: %s", msg)
	}
	if !strings.Contains(msg, `model "gpt-5.6-sol" is not available at this provider`) {
		t.Errorf("error still gives the user nothing to act on: %s", msg)
	}
	if !strings.Contains(msg, "claude-fable-5") {
		t.Errorf("error does not list the models the provider does serve: %s", msg)
	}
}
