package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"sort"
)

type anthropicPrefixKey struct{}

// Hash the actual wire prefix once per message, including unknown native
// fields. Canonical JSON avoids treating object key order as a history edit.
func writeAnthropicPrefix(h hash.Hash, value any) {
	raw, _ := json.Marshal(value)
	var decoded any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&decoded) == nil {
		raw, _ = json.Marshal(decoded)
	}
	_, _ = h.Write(raw)
	_, _ = h.Write([]byte{0})
}

func anthropicPrefixHash(system string, tools any) hash.Hash {
	h := sha256.New()
	writeAnthropicPrefix(h, system)
	writeAnthropicPrefix(h, tools)
	return h
}

func anthropicRequestPrefix(body []byte) string {
	var req struct {
		System   string
		Tools    json.RawMessage
		Messages []json.RawMessage
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	if len(req.Tools) == 0 {
		req.Tools = json.RawMessage("null")
	}
	h := anthropicPrefixHash(req.System, req.Tools)
	for _, m := range req.Messages {
		writeAnthropicPrefix(h, m)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Prefix edits (compaction, tool discovery, routing or a changed reminder)
// invalidate signed state. Preserve the original non-thinking blocks and
// archive; never retry an invalid signature or send opaque state as prose.
func protectAnthropicPrefixes(req *anthropicRequest) {
	h := anthropicPrefixHash(req.System, req.Tools)
	for i := range req.Messages {
		m := &req.Messages[i]
		invalid := false
		for _, b := range m.Content {
			if b.Prefix != "" && b.Prefix != hex.EncodeToString(h.Sum(nil)) {
				invalid = true
				break
			}
		}
		if invalid {
			blocks := make([]anthropicContentBlock, 0, len(m.Content))
			for _, b := range m.Content {
				var kind struct{ Type string }
				_ = json.Unmarshal(b.Raw, &kind)
				if kind.Type != "thinking" && kind.Type != "redacted_thinking" {
					blocks = append(blocks, b)
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: "[no visible answer]"})
			}
			m.Content = blocks
		}
		writeAnthropicPrefix(h, *m)
	}
}

func anthropicNativeContent(m Message, model string) ([]anthropicContentBlock, bool) {
	for _, p := range m.Parts {
		b := p.Reasoning
		if p.Type != PartTypeReasoning || b == nil || b.Format != ReasoningAnthropic || b.Model != model {
			continue
		}
		var native struct {
			Type    string
			Content []json.RawMessage
		}
		if json.Unmarshal(b.Data, &native) != nil || native.Type != "assistant" {
			continue
		}
		out := make([]anthropicContentBlock, 0, len(native.Content))
		for _, raw := range native.Content {
			out = append(out, anthropicContentBlock{Raw: raw, Prefix: b.Prefix})
		}
		return out, true
	}
	return nil, false
}

func finishAnthropicReasoning(ctx context.Context, completed map[int]json.RawMessage, model, base string) *ReasoningBlock {
	indices := make([]int, 0, len(completed))
	hasThinking := false
	for i, raw := range completed {
		indices = append(indices, i)
		var b struct{ Type string }
		_ = json.Unmarshal(raw, &b)
		hasThinking = hasThinking || b.Type == "thinking" || b.Type == "redacted_thinking"
	}
	if !hasThinking {
		return nil
	}
	sort.Ints(indices)
	content := make([]json.RawMessage, 0, len(indices))
	for _, i := range indices {
		content = append(content, completed[i])
	}
	raw, _ := json.Marshal(struct {
		Type    string            `json:"type"`
		Content []json.RawMessage `json:"content"`
	}{Type: "assistant", Content: content})
	block := nativeReasoning(ReasoningAnthropic, model, base, raw)
	block.Prefix, _ = ctx.Value(anthropicPrefixKey{}).(string)
	return block
}
