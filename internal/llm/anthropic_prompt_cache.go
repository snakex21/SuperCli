package llm

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

type anthropicCacheControl struct {
	Type string `json:"type"`
}

// Compatible gateways may reject automatic caching. Enable it only on the
// native API, and avoid cache-write premiums on one-shot auxiliary prompts.
func anthropicConversationCaching(ctx context.Context, base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() != "api.anthropic.com" ||
		(u.Port() != "" && u.Port() != "443") || u.User != nil ||
		u.Path != "/v1" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	switch PurposeFromContext(ctx) {
	case "", PurposeMain, PurposeTask:
		return true
	default:
		return false
	}
}

// Automatic caching handles an append-only conversation. SuperCli's refreshed
// tail is not persisted, so explicitly cache the preceding conversation block
// instead: automatic placement after that tail would pay writes without hits.
// The result requests a stable-system breakpoint only when no preceding
// conversation block can carry it. Internal system text stays a string so
// signed-prefix checks retain the same content representation.
func applyAnthropicPromptCache(req *anthropicRequest, hasReminders bool) bool {
	cache := &anthropicCacheControl{Type: "ephemeral"}
	if !hasReminders {
		req.CacheControl = cache
		return false
	}
	// Role demotion can merge the refreshed tail into the preceding plain
	// user message. Conservatively exclude the complete final wire message.
	for i := len(req.Messages) - 2; i >= 0; i-- {
		for j := len(req.Messages[i].Content) - 1; j >= 0; j-- {
			block := &req.Messages[i].Content[j]
			// Opaque signed blocks are replayed byte-for-byte; never attach
			// transport metadata to their raw envelope or thinking text.
			if block.Raw != nil {
				continue
			}
			switch block.Type {
			case "text":
				if block.Text == "" {
					continue
				}
			case "image", "tool_use", "tool_result":
			default:
				continue
			}
			block.CacheControl = cache
			return false
		}
	}
	// On the first turn the reminder may share the only user message. Cache
	// the stable system prefix rather than dropping caching entirely or
	// including the changing reminder in the write. Tools precede system in
	// Anthropic's rendered prefix and therefore remain covered unchanged.
	return strings.TrimSpace(req.System) != ""
}

func marshalAnthropicPromptCache(req anthropicRequest, cacheSystem bool) ([]byte, error) {
	if !cacheSystem {
		return json.Marshal(req)
	}
	type plainRequest anthropicRequest
	return json.Marshal(struct {
		*plainRequest
		System []anthropicContentBlock `json:"system"`
	}{
		plainRequest: (*plainRequest)(&req),
		System: []anthropicContentBlock{{Type: "text", Text: req.System,
			CacheControl: &anthropicCacheControl{Type: "ephemeral"}}},
	})
}

// Normalize only the single text block our fallback emits. Other native
// arrays may carry additional semantic content and are not equivalent to the
// legacy string prefix. Cache placement alone never changes that prefix.
func anthropicCacheSystemText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) != 1 {
		return "", false
	}
	for key := range blocks[0] {
		if key != "type" && key != "text" && key != "cache_control" {
			return "", false
		}
	}
	var kind string
	var blockText *string
	if json.Unmarshal(blocks[0]["type"], &kind) != nil || kind != "text" ||
		json.Unmarshal(blocks[0]["text"], &blockText) != nil || blockText == nil {
		return "", false
	}
	return *blockText, true
}
