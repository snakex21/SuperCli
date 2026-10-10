package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAnthropicPromptCacheEndpointAndPurpose(t *testing.T) {
	for _, tc := range []struct {
		base, purpose string
		want          bool
	}{
		{"https://api.anthropic.com/v1", "", true},
		{"https://api.anthropic.com:443/v1", PurposeTask, true},
		{"https://api.anthropic.com/v1", PurposeMain, true},
		{"https://api.anthropic.com/v1", PurposeCompact, false},
		{"https://api.anthropic.com/v1", PurposeProbe, false},
		{"https://api.anthropic.com/v1", PurposeMemory, false},
		{"https://api.anthropic.com/v1", PurposeReflect, false},
		{"https://api.anthropic.com/v1", PurposeTitle, false},
		{"https://proxy.invalid/v1", "", false},
		{"https://opencode.ai/zen/v1", "", false},
		{"https://anyrouter.top/v1", "", false},
		{"http://api.anthropic.com/v1", "", false},
		{"https://api.anthropic.com.evil.invalid/v1", "", false},
		{"https://api.anthropic.com@evil.invalid/v1", "", false},
		{"https://api.anthropic.com:8443/v1", "", false},
		{"https://api.anthropic.com/proxy/v1", "", false},
		{"https://api.anthropic.com/v1?route=proxy", "", false},
	} {
		if got := anthropicConversationCaching(WithPurpose(context.Background(), tc.purpose), tc.base); got != tc.want {
			t.Errorf("base=%q purpose=%q: cache=%v want %v", tc.base, tc.purpose, got, tc.want)
		}
	}
}

func TestAnthropicPromptCacheHTTPPreservesProxyWire(t *testing.T) {
	for _, base := range []string{"", "https://api.anthropic.com/v1/messages", "https://proxy.invalid/v1", "https://opencode.ai/zen/v1", "https://anyrouter.top/v1"} {
		t.Run(base, func(t *testing.T) {
			msgs := []Message{{Role: RoleSystem, Content: "stable instructions"}, {Role: RoleUser, Content: strings.Repeat("stable context ", 400)}, {Role: RoleAssistant, Content: "Inspecting the context."}, {Role: RoleSystem, Content: "Current local date/time: 2026-10-08 12:00"}}
			tools := []ToolDef{{Name: "read_file", Schema: `{"type":"object"}`}}
			legacy, err := buildAnthropicRequestWithSampling("claude-fixture", msgs, tools, true, 4096, Sampling{})
			if err != nil {
				t.Fatal(err)
			}
			var body []byte
			calls := 0
			p, err := NewAnthropic(AnthropicConfig{BaseURL: base, Model: "claude-fixture", HTTPClient: &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				body, err = io.ReadAll(req.Body)
				return anthropicOwnedBodyResponse(req, http.StatusOK, "data: {\"type\":\"message_stop\"}\n\n"), err
			})}})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.Complete(context.Background(), msgs, tools)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range drainDeltas(t, ch) {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
			}
			if calls != 1 {
				t.Fatalf("cache enablement made %d HTTP calls, want one", calls)
			}
			if !anthropicConversationCaching(context.Background(), p.cfg.BaseURL) {
				if !bytes.Equal(body, legacy) {
					t.Fatal("proxy request bytes changed")
				}
				return
			}
			var got, want anthropicRequest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(legacy, &want); err != nil {
				t.Fatal(err)
			}
			if got.CacheControl != nil || got.Messages[2].Content[0].CacheControl != nil {
				t.Fatal("refreshed reminder must remain outside the cache write")
			}
			if cache := got.Messages[1].Content[0].CacheControl; cache == nil || cache.Type != "ephemeral" {
				t.Fatal("missing cache marker at the persistent conversation boundary")
			}
			got.Messages[1].Content[0].CacheControl = nil
			if !reflect.DeepEqual(got, want) {
				t.Fatal("caching changed prompt content")
			}
			t.Logf("cache wire addition: %d bytes; prompt content unchanged", len(body)-len(legacy))
		})
	}
}

func TestAnthropicPromptCacheAutomaticAndLegacySignedReplay(t *testing.T) {
	model := "claude-fixture"
	msgs := []Message{{Role: RoleSystem, Content: "stable"}, {Role: RoleUser, Content: "question"}}
	plain, err := buildAnthropicRequestWithCaching(model, msgs, nil, true, 4096, Sampling{}, true)
	if err != nil {
		t.Fatal(err)
	}
	var automatic anthropicRequest
	if err := json.Unmarshal(plain, &automatic); err != nil {
		t.Fatal(err)
	}
	if automatic.CacheControl == nil || automatic.CacheControl.Type != "ephemeral" {
		t.Fatal("plain append-only conversation must use automatic caching")
	}
	legacy, err := buildAnthropicRequestWithSampling(model, msgs, nil, true, 4096, Sampling{})
	if err != nil {
		t.Fatal(err)
	}
	if anthropicRequestPrefix(plain) != anthropicRequestPrefix(legacy) {
		t.Fatal("automatic caching invalidated a legacy prefix")
	}
	data := []byte(`{"type":"assistant","content":[{"type":"thinking","thinking":"signed exact text","signature":"opaque-signature","future":{"cache_control":"preserve nested data"}},{"type":"text","text":"Done"}]}`)
	native := nativeReasoning(ReasoningAnthropic, model, "https://api.anthropic.com/v1", data)
	native.Prefix = anthropicRequestPrefix(legacy)
	msgs = append(msgs, Message{Role: RoleAssistant, Content: "opening step"})
	for _, firstCache := range []bool{false, true} {
		first, err := buildAnthropicRequestWithCaching(model, append(append([]Message{}, msgs...), Message{Role: RoleSystem, Content: "old reminder"}), nil, true, 4096, Sampling{}, firstCache)
		if err != nil {
			t.Fatal(err)
		}
		native.Prefix = anthropicRequestPrefix(first)
		next := append(append([]Message{}, msgs...),
			Message{Role: RoleSystem, Content: "old reminder"},
			Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: native}}},
			Message{Role: RoleUser, Content: "continue"},
			Message{Role: RoleSystem, Content: "new reminder"})
		body, err := buildAnthropicRequestWithCaching(model, next, nil, true, 4096, Sampling{}, true)
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			Messages []struct {
				Content []json.RawMessage
			}
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		var original struct{ Content []json.RawMessage }
		if err := json.Unmarshal(data, &original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(req.Messages[3].Content, original.Content) || !bytes.Equal(native.Data, data) {
			t.Fatalf("cache marker movement changed signed blocks; original cached=%v", firstCache)
		}
		if strings.Contains(string(req.Messages[len(req.Messages)-1].Content[0]), "cache_control") {
			t.Fatal("new reminder acquired a cache marker")
		}
		legacyNext, err := buildAnthropicRequestWithSampling(model, next, nil, true, 4096, Sampling{})
		if err != nil || anthropicRequestPrefix(body) != anthropicRequestPrefix(legacyNext) {
			t.Fatal("explicit cache markers changed the content prefix hash")
		}
	}
}

func TestAnthropicPromptCacheMergedReminderRemainsUncached(t *testing.T) {
	for _, stamp := range []string{"old reminder", "new reminder"} {
		msgs := []Message{{Role: RoleSystem, Content: "stable instructions"}, {Role: RoleUser, Content: "question"}, {Role: RoleSystem, Content: stamp}}
		body, err := buildAnthropicRequestWithCaching("claude-fixture", msgs, nil, true, 4096, Sampling{}, true)
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			System       json.RawMessage
			Messages     []anthropicMessage
			CacheControl *anthropicCacheControl `json:"cache_control"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Content[0].Text, stamp) {
			t.Fatal("fixture must exercise reminder merged with the preceding user message")
		}
		if req.CacheControl != nil || req.Messages[0].Content[0].CacheControl != nil {
			t.Fatal("merged refreshed reminder must not acquire a cache marker")
		}
		var blocks []anthropicContentBlock
		if err := json.Unmarshal(req.System, &blocks); err != nil || len(blocks) != 1 ||
			blocks[0].Text != "stable instructions" || blocks[0].CacheControl == nil {
			t.Fatal("first turn must cache the stable system before the merged reminder")
		}
	}
}

func BenchmarkAnthropicPromptCacheEncode(b *testing.B) {
	msgs := []Message{{Role: RoleSystem, Content: "stable instructions"}, {Role: RoleUser, Content: strings.Repeat("stable context ", 22000)}, {Role: RoleAssistant, Content: "opening step"}, {Role: RoleSystem, Content: "Current local date/time: 2026-10-08 12:00"}}
	for _, tc := range []struct {
		name  string
		cache bool
	}{{"legacy", false}, {"cache", true}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(msgs[1].Content)))
			for i := 0; i < b.N; i++ {
				if _, err := buildAnthropicRequestWithCaching("claude-fixture", msgs, nil, true, 4096, Sampling{}, tc.cache); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
