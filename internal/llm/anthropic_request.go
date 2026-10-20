package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type anthropicRequest struct {
	Model        string                 `json:"model"`
	MaxTokens    int                    `json:"max_tokens"`
	Stream       bool                   `json:"stream"`
	System       string                 `json:"system,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	Tools        []anthropicTool        `json:"tools,omitempty"`
	Thinking     *anthropicThinking     `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
	// Sampling. Pointers + omitempty: unset stays absent from the body.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	// Adaptive models hide thinking text unless asked for a summary.
	Display string `json:"display,omitempty"`
}

// anthropicOutputConfig steers adaptive thinking depth instead of a budget.
type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Prefix       string                 `json:"-"`
	Raw          json.RawMessage        `json:"-"`
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        map[string]any         `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      string                 `json:"content,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func buildAnthropicRequest(model string, msgs []Message, tools []ToolDef, vision bool, maxTokens int) ([]byte, error) {
	return buildAnthropicRequestWithSampling(model, msgs, tools, vision, maxTokens, Sampling{})
}

func buildAnthropicRequestWithSampling(model string, msgs []Message, tools []ToolDef, vision bool, maxTokens int, sampling Sampling) ([]byte, error) {
	return buildAnthropicRequestWithCaching(model, msgs, tools, vision, maxTokens, sampling, false)
}

func buildAnthropicRequestWithCaching(model string, msgs []Message, tools []ToolDef, vision bool, maxTokens int, sampling Sampling, cacheConversation bool, contexts ...context.Context) ([]byte, error) {
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := anthropicRequest{Model: model, MaxTokens: maxTokens, Stream: true}
	req.Thinking, req.OutputConfig = anthropicThinkingForModel(model, maxTokens, contexts...)
	req.MaxTokens = anthropicAdaptiveMaxTokens(req.Thinking, req.MaxTokens)
	// Extended thinking fixes the sampling: the API rejects top_p/top_k
	// and any temperature other than 1 while thinking is on. Silently
	// skipping the parameters is better than turning every request into
	// a 400 for a setting the user meant for the local models.
	if req.Thinking == nil {
		req.Temperature = sampling.Temperature
		req.TopP = sampling.TopP
		req.TopK = sampling.TopK
	}
	for _, t := range tools {
		inputSchema, err := toolSchemaForRequest(t.Schema, toolSchemaAnthropic)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}
		req.Tools = append(req.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: inputSchema})
	}
	// Only the leading run of system messages may be hoisted into
	// req.System; later system messages (freshness stamp, reflection
	// checkpoints, ...) stay in place as <system-reminder> user turns
	// so the prompt prefix stays append-only for prompt caching.
	// Request-only reminders are refreshed between calls and must stay outside
	// the cache write. Record their boundary before the role demotion below.
	cacheEnd := len(msgs)
	for cacheEnd > 0 && msgs[cacheEnd-1].Role == RoleSystem {
		cacheEnd--
	}
	hasReminders := cacheEnd < len(msgs)
	msgs = repairToolCallIDs(demoteMidConversationSystemMessages(msgs))
	var system []string
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			system = append(system, messageText(m))
		case RoleAssistant:
			blocks, err := anthropicAssistantBlocks(m)
			if native, ok := anthropicNativeContent(m, model); ok {
				blocks = native
			}
			if err != nil {
				return nil, err
			}
			if len(blocks) > 0 {
				req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: blocks})
			}
		case RoleTool:
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: []anthropicContentBlock{{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content,
			}}})
		default:
			blocks, err := anthropicUserBlocks(m, vision)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: blocks})
		}
	}
	req.System = strings.Join(system, "\n\n")
	protectAnthropicPrefixes(&req)
	cacheSystem := false
	if cacheConversation {
		cacheSystem = applyAnthropicPromptCache(&req, hasReminders)
	}
	return marshalAnthropicPromptCache(req, cacheSystem)
}

func anthropicThinkingForModel(model string, maxTokens int, contexts ...context.Context) (*anthropicThinking, *anthropicOutputConfig) {
	effort := ReasoningEffortForModel(model, contexts...)
	if effort == "" || effort == "none" {
		return nil, nil
	}
	return anthropicThinkingForEffort(model, effort, maxTokens)
}

func anthropicThinkingForEffort(model, effort string, maxTokens int) (*anthropicThinking, *anthropicOutputConfig) {
	if anthropicUsesAdaptiveThinking(model) {
		return &anthropicThinking{Type: "adaptive", Display: "summarized"}, &anthropicOutputConfig{Effort: anthropicAdaptiveEffort(effort)}
	}
	budget := reasoningBudgetTokens(effort, maxTokens)
	if budget <= 0 {
		return nil, nil
	}
	return &anthropicThinking{Type: "enabled", BudgetTokens: budget}, nil
}

// anthropicThinkingMode holds live evidence ("adaptive"/"enabled") from a
// backend that rejected the other thinking type for a model.
var anthropicThinkingMode sync.Map

// Claude 4.6 and newer reject or deprecate budget-based thinking: depth is
// set with output_config.effort. Only the older generations listed below
// still require "enabled" + budget_tokens. Unknown (future) names default to
// adaptive; a backend rejection is learned and overrides this guess.
func anthropicUsesAdaptiveThinking(model string) bool {
	if mode, ok := anthropicThinkingMode.Load(normalizeReasoningModel(model)); ok {
		return mode == "adaptive"
	}
	m := reasoningModelFamily(model)
	if strings.HasPrefix(m, "claude-3") || strings.HasPrefix(m, "claude-2") || strings.HasPrefix(m, "claude-instant") {
		return false
	}
	for _, old := range []string{"-4-5", "-4-1", "-4-0", "-4-2"} {
		if strings.Contains(m, old) {
			return false
		}
	}
	for _, family := range []string{"claude-opus-4", "claude-sonnet-4", "claude-haiku-4"} {
		if m == family || strings.HasPrefix(m, family+"-2025") {
			return false
		}
	}
	return true
}

// rememberAnthropicThinkingMode records which thinking type a 400 response
// asked for. It returns false when the message names no thinking type.
func rememberAnthropicThinkingMode(model string, body []byte) bool {
	low := strings.ToLower(string(body))
	var mode string
	switch {
	case strings.Contains(low, `"thinking.type.enabled" is not supported`), strings.Contains(low, "use \\\"thinking.type.adaptive\\\""), strings.Contains(low, `use "thinking.type.adaptive"`):
		mode = "adaptive"
	case strings.Contains(low, `"thinking.type.adaptive" is not supported`), strings.Contains(low, "adaptive") && strings.Contains(low, "budget_tokens") && strings.Contains(low, "not supported"):
		mode = "enabled"
	default:
		return false
	}
	if anthropicUsesAdaptiveThinking(model) == (mode == "adaptive") {
		return false // already sending what the backend asked for
	}
	anthropicThinkingMode.Store(normalizeReasoningModel(model), mode)
	return true
}

// anthropicAdaptiveMinOutput is the output ceiling used with adaptive
// thinking. Thinking and the visible answer share max_tokens; with the old
// 4096 default the model spent the whole limit thinking and returned no text.
// It is a ceiling, not a reservation: unused tokens cost nothing.
const anthropicAdaptiveMinOutput = 32000

func anthropicAdaptiveMaxTokens(thinking *anthropicThinking, maxTokens int) int {
	if thinking != nil && thinking.Type == "adaptive" && maxTokens < anthropicAdaptiveMinOutput {
		return anthropicAdaptiveMinOutput
	}
	return maxTokens
}

// Adaptive effort accepts low..max; there is no "minimal".
func anthropicAdaptiveEffort(effort string) string {
	if effort == "minimal" {
		return "low"
	}
	return effort
}

func reasoningBudgetTokens(effort string, maxTokens int) int {
	if maxTokens < 2048 {
		maxTokens = 2048
	}
	budget := 1024
	switch effort {
	case "minimal", "low":
		budget = maxTokens / 4
	case "medium":
		budget = maxTokens / 2
	case "high":
		budget = (maxTokens * 3) / 4
	case "xhigh":
		budget = maxTokens - 1024
	case "max":
		// Manual thinking requires budget_tokens < max_tokens. This is
		// a ceiling, not a reservation; unused thinking tokens remain for text.
		budget = maxTokens - 1
	}
	if budget < 1024 {
		budget = 1024
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	return budget
}

func anthropicUserBlocks(m Message, vision bool) ([]anthropicContentBlock, error) {
	if len(m.Parts) == 0 {
		return []anthropicContentBlock{{Type: "text", Text: m.Content}}, nil
	}
	var blocks []anthropicContentBlock
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText:
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: p.Text})
		case PartTypeImage:
			if !vision {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: imageInputOmittedPlaceholder})
				continue
			}
			url, err := resolveImageURL(p.Image)
			if err != nil {
				return nil, err
			}
			src := anthropicImageSource{}
			if !strings.HasPrefix(url, "data:") {
				src = anthropicImageSource{Type: "url", URL: url}
			} else {
				mediaType, data, ok := parseDataURI(url)
				if !ok || mediaType == "" || data == "" {
					return nil, fmt.Errorf("image part: incomplete Anthropic image source")
				}
				src = anthropicImageSource{Type: "base64", MediaType: mediaType, Data: data}
			}
			blocks = append(blocks, anthropicContentBlock{Type: "image", Source: &src})
		}
	}
	return blocks, nil
}

func parseDataURI(uri string) (mediaType, data string, ok bool) {
	const marker = ";base64,"
	if !strings.HasPrefix(uri, "data:") {
		return "", "", false
	}
	i := strings.Index(uri, marker)
	if i < 0 {
		return "", "", false
	}
	return uri[len("data:"):i], uri[i+len(marker):], true
}

func anthropicAssistantBlocks(m Message) ([]anthropicContentBlock, error) {
	var blocks []anthropicContentBlock
	if text := messageText(m); text != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
	}
	for _, tc := range m.ToolCalls {
		input := map[string]any{}
		if err := json.Unmarshal([]byte(providerSafeToolArguments(tc.Arguments)), &input); err != nil {
			return nil, fmt.Errorf("anthropic tool_use %s args: %w", tc.Name, err)
		}
		blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
	}
	return blocks, nil
}
