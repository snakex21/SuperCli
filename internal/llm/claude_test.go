package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type claudeTestTokens struct {
	refreshed bool
	forced    int
}

func (s *claudeTestTokens) AccessToken(_ context.Context, force bool) (string, error) {
	if force {
		s.refreshed = true
		s.forced++
	}
	if s.refreshed {
		return "fresh", nil
	}
	return "stale", nil
}

func TestClaudeMessagesRefreshOnceAndKeepOAuthHeaders(t *testing.T) {
	tokens := &claudeTestTokens{}
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "" || !strings.Contains(r.Header.Get("anthropic-beta"), "oauth-2025-04-20") || r.Header.Get("anthropic-version") == "" {
			t.Error("incorrect OAuth wire headers")
		}
		if r.Header.Get("Authorization") == "Bearer stale" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh" {
			t.Error("fresh token not used")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	p, err := NewAnthropic(AnthropicConfig{BaseURL: upstream.URL + "/v1", Model: "claude-test", APIKey: "must-not-send", Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	if requests != 2 || tokens.forced != 1 {
		t.Fatalf("requests=%d, refreshes=%d", requests, tokens.forced)
	}
}

func TestClaudeModelDiscoveryRefreshAndPagination(t *testing.T) {
	tokens := &claudeTestTokens{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" || !strings.Contains(r.Header.Get("anthropic-beta"), "oauth-2025-04-20") {
			t.Error("incorrect catalog auth")
		}
		if r.Header.Get("Authorization") == "Bearer stale" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("after_id") == "first" {
			fmt.Fprint(w, `{"data":[{"id":"second"}],"has_more":false}`)
		} else {
			fmt.Fprint(w, `{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`)
		}
	}))
	defer upstream.Close()
	ids, err := listClaudeModels(context.Background(), upstream.URL, tokens, upstream.Client())
	if err != nil || strings.Join(ids, ",") != "first,second" || tokens.forced != 1 {
		t.Fatalf("models=%v, error=%v, refreshes=%d", ids, err, tokens.forced)
	}
}

func TestClaudeCannotSendPortableTokensToCustomEndpoint(t *testing.T) {
	if _, err := NewClaude(AnthropicConfig{BaseURL: "https://proxy.invalid/v1", Model: "claude-test"}, t.TempDir()); err == nil {
		t.Fatal("accepted arbitrary OAuth destination")
	}
	if _, err := NewClaude(AnthropicConfig{Model: "claude-test"}, ""); err == nil {
		t.Fatal("accepted empty data directory")
	}
}

func TestClaudeOAuthToolNamesAndSignedHistoryRemainCanonical(t *testing.T) {
	msgs := []Message{{Role: RoleSystem, Content: "SuperCli instructions"}, {Role: RoleUser, Content: "Run a tool"}}
	tools := []ToolDef{{Name: "bash", Schema: `{"type":"object","properties":{}}`}, {Name: "custom_bash", Schema: `{"type":"object","properties":{}}`}}
	body, err := buildAnthropicRequest("claude-test", msgs, tools, false, 4096)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := claudeOAuthRequest(body, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), claudeOAuthPreamble) || !strings.Contains(string(wire), `"name":"custom_custom_bash"`) {
		t.Fatalf("OAuth compatibility missing: %s", wire)
	}
	canonical, err := claudeOAuthRequest(wire, false)
	if err != nil || anthropicRequestPrefix(canonical) != anthropicRequestPrefix(body) {
		t.Fatal("OAuth adapter changed signed-history prefix")
	}
	// Names with an existing custom_ prefix must stay distinct; large tool
	// argument integers must survive the JSON adaptation without float rounding.
	large := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"custom_bash","input":{"n":9007199254740993}}]}]}`)
	encoded, err := claudeOAuthRequest(large, true)
	if err != nil || !strings.Contains(string(encoded), "9007199254740993") {
		t.Fatal("large integer corrupted")
	}
	p := &AnthropicProvider{cfg: AnthropicConfig{Model: "claude-test", BaseURL: "https://api.anthropic.com/v1", Tokens: &claudeTestTokens{}}}
	sse := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan","signature":"sig"}}`, "",
		`data: {"type":"content_block_stop","index":0}`, "",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t","name":"custom_bash","input":{"n":9007199254740993}}}`, "",
		`data: {"type":"content_block_stop","index":1}`, "",
		`data: {"type":"message_stop"}`, "", "",
	}, "\n")
	out := make(chan Delta, 16)
	ctx := context.WithValue(context.Background(), anthropicPrefixKey{}, anthropicRequestPrefix(canonical))
	if err := p.streamSSE(ctx, strings.NewReader(sse), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var native *ReasoningBlock
	var call *ToolCall
	for delta := range out {
		if delta.ToolCall != nil {
			call = delta.ToolCall
		}
		if delta.NativeReasoning != nil {
			native = delta.NativeReasoning
		}
	}
	if call == nil || call.Name != "bash" || native == nil {
		t.Fatal("OAuth tool response not normalized")
	}
	if strings.Contains(string(native.Data), "custom_bash") || !json.Valid(native.Data) {
		t.Fatal("wire names leaked into saved native history")
	}
	msgs = append(msgs, Message{Role: RoleAssistant, ToolCalls: []ToolCall{*call}, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: native}}}, Message{Role: RoleTool, ToolCallID: "t", Content: "done"})
	replay, err := buildAnthropicRequest("claude-test", msgs, tools, false, 4096)
	if err != nil || !strings.Contains(string(replay), `"signature":"sig"`) {
		t.Fatalf("signed reasoning lost on next turn: %s / %v", replay, err)
	}
}
