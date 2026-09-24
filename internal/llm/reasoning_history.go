package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ReasoningBlock is provider-native continuation state, separate from the
// display transcript. Data is opaque for signed/encrypted formats: never
// summarize, edit or convert it to an instruction. It is persisted in Parts.
type ReasoningBlock struct {
	Format string
	Model  string
	Scope  string
	Data   json.RawMessage
	Tokens int
	Prefix string // Exact request prefix for signed Anthropic continuation.
}

const (
	ReasoningChat      = "chat"
	ReasoningResponses = "responses"
	ReasoningAnthropic = "anthropic"
)

func reasoningScope(base string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(base, "/")))
	return hex.EncodeToString(sum[:16])
}

func nativeReasoning(format, model, base string, raw []byte) *ReasoningBlock {
	return &ReasoningBlock{Format: format, Model: model, Scope: reasoningScope(base), Data: append(json.RawMessage(nil), raw...)}
}

func (b *ReasoningBlock) Validate() error {
	if b == nil || b.Model == "" || b.Scope == "" || !json.Valid(b.Data) {
		return fmt.Errorf("reasoning part: invalid origin or payload")
	}
	switch b.Format {
	case ReasoningChat, ReasoningResponses, ReasoningAnthropic:
	default:
		return fmt.Errorf("reasoning part: unknown format %q", b.Format)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b.Data, &fields) != nil || fields == nil {
		return fmt.Errorf("reasoning part: payload must be an object")
	}
	if b.Format == ReasoningChat {
		if len(fields) != 1 {
			return fmt.Errorf("reasoning part: expected one native chat field")
		}
		for key, value := range fields {
			if key != "reasoning_content" && key != "reasoning" && key != "reasoning_text" {
				return fmt.Errorf("reasoning part: unsupported chat field")
			}
			var text string
			if json.Unmarshal(value, &text) != nil {
				return fmt.Errorf("reasoning part: chat reasoning must be text")
			}
		}
	} else {
		var kind string
		_ = json.Unmarshal(fields["type"], &kind)
		if b.Format == ReasoningResponses && kind != "reasoning" {
			return fmt.Errorf("reasoning part: expected reasoning item")
		}
		if b.Format == ReasoningAnthropic && kind == "assistant" {
			var content []map[string]json.RawMessage
			if json.Unmarshal(fields["content"], &content) != nil || len(content) == 0 {
				return fmt.Errorf("reasoning part: missing native assistant content")
			}
			hasThinking := false
			for _, part := range content {
				var t string
				if json.Unmarshal(part["type"], &t) != nil || t == "" {
					return fmt.Errorf("reasoning part: invalid content block")
				}
				hasThinking = hasThinking || t == "thinking" || t == "redacted_thinking"
			}
			if !hasThinking {
				return fmt.Errorf("reasoning part: native assistant has no thinking")
			}
		}
		if b.Format == ReasoningAnthropic && kind != "thinking" && kind != "redacted_thinking" && kind != "assistant" {
			return fmt.Errorf("reasoning part: expected thinking block")
		}
	}
	return nil
}

func (b *ReasoningBlock) EstimateTokens() int {
	if b == nil {
		return 0
	}
	if b.Tokens > 0 {
		return b.Tokens
	}
	if b.Format == ReasoningAnthropic {
		var native struct{ Content []json.RawMessage }
		if json.Unmarshal(b.Data, &native) == nil && len(native.Content) > 0 {
			n := 0
			for _, raw := range native.Content {
				var part struct{ Type string }
				_ = json.Unmarshal(raw, &part)
				if part.Type == "thinking" || part.Type == "redacted_thinking" {
					n += nonWhitespaceLen(string(raw))
				}
			}
			return n / estBytesPerToken
		}
	}
	return nonWhitespaceLen(string(b.Data)) / estBytesPerToken
}

// filterNativeReasoning prevents replay across endpoints, models or protocols.
// Only allocate for messages containing incompatible state; do not mutate the
// persisted/UI copy. Native state never becomes visible text on a model swap.
func filterNativeReasoning(msgs []Message, format, model, base string) []Message {
	var out []Message
	scope := reasoningScope(base)
	for i, m := range msgs {
		changed := false
		for _, p := range m.Parts {
			if p.Type == PartTypeReasoning && (m.Role != RoleAssistant || p.Reasoning == nil || p.Reasoning.Format != format || p.Reasoning.Model != model || p.Reasoning.Scope != scope) {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		if out == nil {
			out = append([]Message(nil), msgs...)
		}
		parts := make([]ContentPart, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.Type != PartTypeReasoning || (m.Role == RoleAssistant && p.Reasoning != nil && p.Reasoning.Format == format && p.Reasoning.Model == model && p.Reasoning.Scope == scope) {
				parts = append(parts, p)
			}
		}
		m.Parts = parts
		if m.Role == RoleAssistant && len(parts) == 0 && m.Content == "" && len(m.ToolCalls) == 0 {
			m.Content = "[no visible answer]"
		}
		out[i] = m
	}
	if out != nil {
		return out
	}
	return msgs
}

func nativePayloads(m Message, format, model string) []json.RawMessage {
	if m.Role != RoleAssistant {
		return nil
	}
	var out []json.RawMessage
	for _, p := range m.Parts {
		b := p.Reasoning
		if p.Type == PartTypeReasoning && b != nil && b.Format == format && b.Model == model && b.Validate() == nil {
			out = append(out, b.Data)
		}
	}
	return out
}

type chatReasoningAccumulator struct {
	field string
	text  strings.Builder
}

func (a *chatReasoningAccumulator) add(delta map[string]json.RawMessage) {
	for _, key := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		var s string
		if json.Unmarshal(delta[key], &s) == nil && s != "" {
			if a.field == "" {
				a.field = key
			}
			if key == a.field {
				a.text.WriteString(s)
			}
			return
		}
	}
}
func (a *chatReasoningAccumulator) block(model, base string) *ReasoningBlock {
	if a.field == "" {
		return nil
	}
	data, _ := json.Marshal(map[string]string{a.field: a.text.String()})
	b := nativeReasoning(ReasoningChat, model, base, data)
	a.field = ""
	a.text.Reset()
	return b
}

func applyChatReasoning(out *openaiReqMsg, m Message, model string) {
	for _, raw := range nativePayloads(m, ReasoningChat, model) {
		var fields map[string]string
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for key, value := range fields {
			s := value
			switch key {
			case "reasoning_content":
				out.ReasoningContent = &s
			case "reasoning":
				out.Reasoning = &s
			case "reasoning_text":
				out.ReasoningText = &s
			}
		}
	}
}

// Raw native items must survive unknown signature/encryption fields exactly.
func (item codexItem) MarshalJSON() ([]byte, error) {
	if item.Raw != nil {
		return item.Raw, nil
	}
	type wire codexItem
	return json.Marshal(wire(item))
}
func (block anthropicContentBlock) MarshalJSON() ([]byte, error) {
	if block.Raw != nil {
		return block.Raw, nil
	}
	type wire anthropicContentBlock
	return json.Marshal(wire(block))
}

type anthropicReasoningAccumulator struct {
	fields    map[string]json.RawMessage
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
}

func newAnthropicReasoning(raw json.RawMessage) *anthropicReasoningAccumulator {
	a := &anthropicReasoningAccumulator{}
	if json.Unmarshal(raw, &a.fields) != nil {
		return nil
	}
	var visible string
	_ = json.Unmarshal(a.fields["text"], &visible)
	a.text.WriteString(visible)
	var text, signature string
	_ = json.Unmarshal(a.fields["thinking"], &text)
	_ = json.Unmarshal(a.fields["signature"], &signature)
	a.thinking.WriteString(text)
	a.signature.WriteString(signature)
	return a
}
func (a *anthropicReasoningAccumulator) block(model, base string) *ReasoningBlock {
	if a == nil {
		return nil
	}
	var kind string
	_ = json.Unmarshal(a.fields["type"], &kind)
	if kind == "text" {
		a.fields["text"], _ = json.Marshal(a.text.String())
	}
	if kind == "thinking" {
		a.fields["thinking"], _ = json.Marshal(a.thinking.String())
		// Keep an explicit empty signature if the upstream sent one; unsigned
		// compatibility backends must not receive a fabricated signature field.
		if _, ok := a.fields["signature"]; ok || a.signature.Len() > 0 {
			a.fields["signature"], _ = json.Marshal(a.signature.String())
		}
	}
	raw, _ := json.Marshal(a.fields)
	return nativeReasoning(ReasoningAnthropic, model, base, raw)
}

// HasNativeReasoning reports replay state independent of visible text.
func (m Message) HasNativeReasoning() bool {
	for _, p := range m.Parts {
		if p.Type == PartTypeReasoning && p.Reasoning != nil {
			return true
		}
	}
	return false
}

// ProjectReasoningHistory aligns context estimates and projections with native
// scope filtering at the transport boundary. Incompatible state must not cause
// an unnecessary handoff summary before a smaller model receives the request.
func ProjectReasoningHistory(provider Provider, msgs []Message) []Message {
	switch p := Unwrap(provider).(type) {
	case *OpenAIProvider:
		return filterNativeReasoning(msgs, ReasoningChat, p.cfg.Model, p.cfg.BaseURL)
	case *ResponsesProvider:
		return ProjectReasoningHistory(p.inner, msgs)
	case *CodexProvider:
		return filterNativeReasoning(msgs, ReasoningResponses, p.cfg.Model, p.cfg.BackendURL)
	case *AnthropicProvider:
		return filterNativeReasoning(msgs, ReasoningAnthropic, p.cfg.Model, p.cfg.BaseURL)
	default:
		return msgs
	}
}
