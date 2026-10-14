package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// zenDeprecatedBody is the live 410 payload observed on 2026-10-14, when
// OpenCode Zen retired mimo-v2.5-free in favour of mimo-v2.6-flash-free.
const zenDeprecatedBody = `{"type":"error","error":{"type":"ModelDeprecated","message":"Model mimo-v2.5-free has been deprecated. Use mimo-v2.6-flash-free instead."},"metadata":{"model":"mimo-v2.5-free","replacement":"mimo-v2.6-flash-free"}}`

// withZenReplacementCache isolates the process-wide successor cache so a
// learned remap from one test cannot leak into another (or into the other
// Zen tests in this package).
func withZenReplacementCache(t *testing.T) {
	t.Helper()
	clearZenModelReplacementCache()
	t.Cleanup(clearZenModelReplacementCache)
}

func TestZenModelDeprecationParsesReplacement(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "live envelope",
			status: 410,
			body:   zenDeprecatedBody,
			want:   "mimo-v2.6-flash-free",
		},
		{
			name:   "metadata wins over message",
			status: 410,
			body:   `{"error":{"type":"ModelDeprecated","message":"gone"},"metadata":{"replacement":"next-free"}}`,
			want:   "next-free",
		},
		{
			name:   "message fallback without metadata",
			status: 410,
			body:   `{"error":{"type":"ModelDeprecated","message":"Model old-free has been deprecated. Use new-free instead."}}`,
			want:   "new-free",
		},
		{
			name:   "plain text body",
			status: 410,
			body:   "Model old-free has been deprecated. Use new-free instead.",
			want:   "new-free",
		},
		{
			name:   "type marker on a non-410 status",
			status: 400,
			body:   `{"error":{"type":"ModelDeprecated","message":"old-free deprecated"},"metadata":{"replacement":"new-free"}}`,
			want:   "new-free",
		},
		{
			name:   "410 without a successor names nothing",
			status: 410,
			body:   `{"error":{"type":"NotFound","message":"gone"}}`,
			want:   "",
		},
		{
			name:   "410 garbage body",
			status: 410,
			body:   "nope",
			want:   "",
		},
		{
			name:   "prose replacement with whitespace is rejected",
			status: 410,
			body:   `{"error":{"type":"ModelDeprecated","message":"has been deprecated. Use a b instead."}}`,
			want:   "",
		},
		{
			name:   "ordinary 404 is not a deprecation",
			status: 404,
			body:   `{"error":{"message":"model not found"}}`,
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := zenModelDeprecation(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("zenModelDeprecation(%d, %s) = %q, want %q", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

func TestRetargetRequestModel(t *testing.T) {
	body := []byte(`{"model":"mimo-v2.5-free","messages":[{"role":"user","content":"hi"}]}`)
	patched, old, ok := retargetRequestModel(body, "mimo-v2.6-flash-free")
	if !ok || old != "mimo-v2.5-free" {
		t.Fatalf("retarget = (old %q, ok %v), want mimo-v2.5-free/true", old, ok)
	}
	var req struct {
		Model    string `json:"model"`
		Messages []any  `json:"messages"`
	}
	if err := json.Unmarshal(patched, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "mimo-v2.6-flash-free" {
		t.Errorf("model = %q, want the replacement", req.Model)
	}
	if len(req.Messages) != 1 {
		t.Errorf("messages dropped by the patch: %d", len(req.Messages))
	}
	// Already retargeted: nothing to do, nothing to remember.
	if _, _, ok := retargetRequestModel(patched, "mimo-v2.6-flash-free"); ok {
		t.Error("retarget of an already-retargeted body reported success")
	}
	if _, _, ok := retargetRequestModel([]byte("not json"), "x-free"); ok {
		t.Error("retarget of a non-JSON body reported success")
	}
	if _, _, ok := retargetRequestModel(body, "bad model id"); ok {
		t.Error("retarget accepted an id with whitespace")
	}
}

func TestZenEffectiveModelRemembersReplacement(t *testing.T) {
	withZenReplacementCache(t)
	const zen = "https://opencode.ai/zen/v1"

	if got := zenEffectiveModel(zen, "mimo-v2.5-free"); got != "mimo-v2.5-free" {
		t.Errorf("unknown model rewritten to %q", got)
	}
	// The stored id is case-normalised, the lookup too.
	rememberZenModelReplacement(zen, "Mimo-V2.5-Free", "mimo-v2.6-flash-free")
	if got := zenEffectiveModel(zen, "mimo-v2.5-free"); got != "mimo-v2.6-flash-free" {
		t.Errorf("learned replacement not applied: %q", got)
	}
	// Another endpoint is never affected by a Zen learning.
	if got := zenEffectiveModel("https://api.example.com/v1", "mimo-v2.5-free"); got != "mimo-v2.5-free" {
		t.Errorf("non-Zen endpoint reused a Zen replacement: %q", got)
	}
	// Garbage never reaches the cache.
	rememberZenModelReplacement(zen, "bad-free", "not a model id")
	if got := zenEffectiveModel(zen, "bad-free"); got != "bad-free" {
		t.Errorf("garbage replacement stored: %q", got)
	}
	// A deprecation chain flattens: ask for A, learn B was replaced by C,
	// the next build of A goes straight to C.
	rememberZenReplacement(zen, "a-free", "b-free", "c-free")
	if got := zenEffectiveModel(zen, "a-free"); got != "c-free" {
		t.Errorf("chain not flattened: %q", got)
	}
	if got := zenEffectiveModel(zen, "b-free"); got != "c-free" {
		t.Errorf("wire id not remembered: %q", got)
	}
}

// TestZenDeprecationHintReplacementFirst pins both halves of the contract:
// a Zen 410 leads with the exact successor id, and no other provider's 410
// is interpreted as one (their prose is not ours to repair).
func TestZenDeprecationHintReplacementFirst(t *testing.T) {
	body := []byte(zenDeprecatedBody)

	hint := providerErrorHint("https://opencode.ai/zen/v1", "mimo-v2.5-free", 410, body)
	if !strings.Contains(hint, `model "mimo-v2.5-free" is deprecated`) {
		t.Errorf("hint does not name the deprecation: %q", hint)
	}
	if !strings.Contains(hint, `use "mimo-v2.6-flash-free" instead`) {
		t.Errorf("hint does not lead with the replacement: %q", hint)
	}
	if strings.Contains(hint, "is not available") || strings.Contains(hint, "available:") {
		t.Errorf("generic unavailable hint displaced the exact replacement: %q", hint)
	}

	// Non-Zen endpoints keep the existing behaviour: no deprecation reading.
	plain := providerErrorHint("https://api.openai.com/v1", "mimo-v2.5-free", 410, body)
	if strings.Contains(plain, "is deprecated") {
		t.Errorf("non-Zen endpoint got a Zen deprecation hint: %q", plain)
	}

	// A Zen 410 that names no successor falls back to the ordinary hint.
	fallback := providerErrorHint("https://opencode.ai/zen/v1", "mimo-v2.5-free", 410,
		[]byte(`{"error":{"type":"NotFound","message":"gone"}}`))
	if strings.Contains(fallback, "is deprecated") {
		t.Errorf("successorless 410 claimed a replacement: %q", fallback)
	}
}

// decodeRequestModel records the "model" field of each request the transport
// saw. Parsing failures come back as an empty id, which the assertions
// report instead of crashing inside the transport goroutine.
func decodeRequestModel(r *http.Request) string {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return ""
	}
	return req.Model
}

func drainStream(t *testing.T, stream <-chan Delta) (content string, notices []string) {
	t.Helper()
	for d := range stream {
		if d.Err != nil {
			t.Fatalf("stream error: %v", d.Err)
		}
		content += d.Content
		if d.Notice != "" {
			notices = append(notices, d.Notice)
		}
	}
	return content, notices
}

func requireNotice(t *testing.T, notices []string, want string) {
	t.Helper()
	for _, n := range notices {
		if strings.Contains(n, want) {
			return
		}
	}
	t.Errorf("no notice containing %q; got %v", want, notices)
}

// TestOpenAIZenDeprecatedModelAutoRemaps is the reported failure on the chat
// transport: the configured mimo-v2.5-free answers 410, the transport must
// retarget to the named successor, tell the user, succeed — and build the
// next turn with the successor directly instead of paying the 410 again.
func TestOpenAIZenDeprecatedModelAutoRemaps(t *testing.T) {
	withZenReplacementCache(t)
	var models []string
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL: "https://opencode.ai/zen/v1",
		Model:   "mimo-v2.5-free",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			models = append(models, decodeRequestModel(r))
			if models[len(models)-1] == "mimo-v2.5-free" {
				return &http.Response{
					StatusCode: http.StatusGone,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(zenDeprecatedBody)),
					Request:    r,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
						"data: [DONE]\n\n")),
				Request: r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOpenCodeSession(context.Background(), "sess-dep-1")

	stream, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content, notices := drainStream(t, stream)
	if content != "ok" {
		t.Errorf("content = %q, want ok", content)
	}
	if len(models) != 2 {
		t.Fatalf("saw %d requests (%v), want the 410 plus one retargeted retry", len(models), models)
	}
	if models[0] != "mimo-v2.5-free" || models[1] != "mimo-v2.6-flash-free" {
		t.Errorf("request models = %v, want [mimo-v2.5-free mimo-v2.6-flash-free]", models)
	}
	requireNotice(t, notices, "deprecated")

	// The learning sticks: the next turn never sends the retired id.
	stream, err = p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi again"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ = drainStream(t, stream); content != "ok" {
		t.Errorf("second content = %q, want ok", content)
	}
	if len(models) != 3 {
		t.Fatalf("saw %d requests, want one more", len(models))
	}
	if models[2] != "mimo-v2.6-flash-free" {
		t.Errorf("second turn sent %q, want the learned successor", models[2])
	}
}

// TestCodexZenDeprecatedModelAutoRemaps is the same contract on the
// Responses transport (muse free models live there).
func TestCodexZenDeprecatedModelAutoRemaps(t *testing.T) {
	withZenReplacementCache(t)
	var models []string
	p, err := NewCodex(CodexConfig{
		BackendURL:           "https://opencode.ai/zen/v1",
		Model:                "mimo-v2.5-free",
		Tokens:               &fakeTokens{access: "tok"},
		StandardResponsesAPI: true,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			models = append(models, decodeRequestModel(r))
			if models[len(models)-1] == "mimo-v2.5-free" {
				return &http.Response{
					StatusCode: http.StatusGone,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(zenDeprecatedBody)),
					Request:    r,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
						"data: {\"type\":\"response.completed\"}\n\n")),
				Request: r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOpenCodeSession(context.Background(), "sess-dep-2")

	stream, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content, notices := drainStream(t, stream)
	if content != "ok" {
		t.Errorf("content = %q, want ok", content)
	}
	if len(models) != 2 || models[0] != "mimo-v2.5-free" || models[1] != "mimo-v2.6-flash-free" {
		t.Fatalf("request models = %v, want [mimo-v2.5-free mimo-v2.6-flash-free]", models)
	}
	requireNotice(t, notices, "deprecated")

	stream, err = p.Complete(ctx, []Message{{Role: RoleUser, Content: "again"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ = drainStream(t, stream); content != "ok" {
		t.Errorf("second content = %q, want ok", content)
	}
	if models[len(models)-1] != "mimo-v2.6-flash-free" {
		t.Errorf("second turn sent %q, want the learned successor", models[len(models)-1])
	}
}

// TestAnthropicZenDeprecatedModelAutoRemaps closes the loop on the Messages
// transport, where the anthropic-transport Zen models are configured.
func TestAnthropicZenDeprecatedModelAutoRemaps(t *testing.T) {
	withZenReplacementCache(t)
	var models []string
	p, err := NewAnthropic(AnthropicConfig{
		BaseURL: "https://opencode.ai/zen/v1",
		Model:   "mimo-v2.5-free",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			models = append(models, decodeRequestModel(r))
			if models[len(models)-1] == "mimo-v2.5-free" {
				return &http.Response{
					StatusCode: http.StatusGone,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(zenDeprecatedBody)),
					Request:    r,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
						"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
						"data: {\"type\":\"message_stop\"}\n\n")),
				Request: r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content, notices := drainStream(t, stream)
	if content != "ok" {
		t.Errorf("content = %q, want ok", content)
	}
	if len(models) != 2 || models[0] != "mimo-v2.5-free" || models[1] != "mimo-v2.6-flash-free" {
		t.Fatalf("request models = %v, want [mimo-v2.5-free mimo-v2.6-flash-free]", models)
	}
	requireNotice(t, notices, "deprecated")

	stream, err = p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ = drainStream(t, stream); content != "ok" {
		t.Errorf("second content = %q, want ok", content)
	}
	if models[len(models)-1] != "mimo-v2.6-flash-free" {
		t.Errorf("second turn sent %q, want the learned successor", models[len(models)-1])
	}
}

// TestNonZenDeprecationIsNotRemapped pins the scope: a 410 from another
// provider surfaces as an error with its own hint, never a silent retarget.
func TestNonZenDeprecationIsNotRemapped(t *testing.T) {
	withZenReplacementCache(t)
	var models []string
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL: "https://api.example.com/v1",
		Model:   "mimo-v2.5-free",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			models = append(models, decodeRequestModel(r))
			return &http.Response{
				StatusCode: http.StatusGone,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(zenDeprecatedBody)),
				Request:    r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawErr error
	for d := range stream {
		if d.Err != nil {
			sawErr = d.Err
		}
	}
	if sawErr == nil {
		t.Fatal("non-Zen 410 did not surface as an error")
	}
	if !strings.Contains(sawErr.Error(), "410") {
		t.Errorf("error lost the status: %v", sawErr)
	}
	if strings.Contains(sawErr.Error(), "is deprecated") {
		t.Errorf("non-Zen error carries the Zen deprecation hint: %v", sawErr)
	}
	if len(models) != 1 {
		t.Errorf("non-Zen endpoint was retried %d times, want 1 (no auto-remap)", len(models))
	}
}
