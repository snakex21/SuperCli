package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPromptCacheScopesSeparateContinuationsAndHelpers(t *testing.T) {
	root := WithOpenCodeSession(context.Background(), "conversation-a")
	worker := WithPromptCacheScope(root, "worker-1")
	workerAgain := WithPromptCacheScope(root, "worker-1")
	other := WithPromptCacheScope(root, "worker-2")
	key := func(ctx context.Context) string { return promptCacheSessionID(ctx, "fallback") }
	if key(root) != "conversation-a" || key(worker) != key(workerAgain) || key(worker) == key(root) || key(worker) == key(other) {
		t.Fatal("main/worker cache identities are not independent and stable")
	}
	for _, purpose := range []string{PurposeCompact, PurposeTitle, PurposeReflect} {
		helper := WithPurpose(worker, purpose)
		if key(helper) == key(worker) || key(helper) == key(WithPurpose(root, purpose)) || key(helper) != key(WithPurpose(workerAgain, purpose)) {
			t.Fatal("helper cache identity collided with a different continuation")
		}
	}
	if openCodeSessionFromContext(worker) != "conversation-a" || key(WithPurpose(root, PurposeMain)) != key(root) {
		t.Fatal("cache scope changed history identity or the foreground key")
	}
	if key(WithPromptCacheScope(worker, "nested-unique-loop")) != key(WithPromptCacheScope(root, "nested-unique-loop")) {
		t.Fatal("resuming a nested worker through a different caller changed its key")
	}
}

func TestPromptCacheScopesReachResponsesWireWithoutChangingContent(t *testing.T) {
	for _, protocol := range []string{"codex", "responses", "zen"} {
		t.Run(protocol, func(t *testing.T) {
			type record struct {
				key, session, zenSession              string
				input, instructions, tools, reasoning json.RawMessage
			}
			var records []record
			client := &http.Client{Transport: responsesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var key string
				_ = json.Unmarshal(body["prompt_cache_key"], &key)
				records = append(records, record{key, r.Header.Get("Session_id"), r.Header.Get("X-OpenCode-Session"), body["input"], body["instructions"], body["tools"], body["reasoning"]})
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")), Request: r}, nil
			})}
			var provider Provider
			var err error
			if protocol == "codex" {
				provider, err = NewCodex(CodexConfig{BackendURL: "https://fixture.invalid", Model: "gpt-5-codex", Tokens: &fakeTokens{access: "synthetic"}, HTTPClient: client})
			} else {
				base := "https://fixture.invalid/v1"
				if protocol == "zen" {
					base = "https://opencode.ai/zen/v1"
				}
				provider, err = NewResponses(ResponsesConfig{BaseURL: base, Model: "gpt-5-codex", HTTPClient: client})
			}
			if err != nil {
				t.Fatal(err)
			}
			root := WithReasoningEffort(WithOpenCodeSession(context.Background(), "conversation-a"), "high")
			worker := WithPurpose(WithPromptCacheScope(root, "worker-1"), PurposeTask)
			contexts := []context.Context{root, WithPurpose(root, PurposeCompact), worker, root, worker, WithPurpose(worker, PurposeCompact)}
			msgs := []Message{{Role: RoleSystem, Content: "Keep all restrictions."}, {Role: RoleUser, Content: "Exact request: zażółć 日本語"}}
			defs := []ToolDef{{Name: "fixture", Description: "Fixture tool.", Schema: `{"type":"object"}`}}
			for _, ctx := range contexts {
				ch, err := provider.Complete(ctx, msgs, defs)
				if err != nil {
					t.Fatal(err)
				}
				for delta := range ch {
					if delta.Err != nil {
						t.Fatal(delta.Err)
					}
				}
			}
			if len(records) != len(contexts) {
				t.Fatal("unexpected number of HTTP requests")
			}
			if records[0].key != records[3].key || records[2].key != records[4].key {
				t.Fatal("continuation keys changed")
			}
			seen := map[string]bool{}
			for _, i := range []int{0, 1, 2, 5} {
				if records[i].key == "" || seen[records[i].key] {
					t.Fatal("cache scopes collided on the wire")
				}
				seen[records[i].key] = true
			}
			for _, got := range records {
				if string(got.input) != string(records[0].input) || string(got.instructions) != string(records[0].instructions) || string(got.tools) != string(records[0].tools) || string(got.reasoning) != string(records[0].reasoning) {
					t.Fatal("cache routing altered content, tools or reasoning")
				}
				if protocol == "codex" && got.session != got.key {
					t.Fatal("subscription cache header/body disagree")
				}
				if protocol == "zen" && (got.zenSession != got.key || !isZenSessionWireID(got.key)) {
					t.Fatal("gateway cache header/body disagree")
				}
			}
		})
	}
}
