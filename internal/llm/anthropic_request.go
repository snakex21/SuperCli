package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
	// Sampling. Pointers + omitempty: unset stays absent from the body.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     map[string]any        `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
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
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := anthropicRequest{Model: model, MaxTokens: maxTokens, Stream: true}
	if thinking := anthropicThinkingForModel(model, maxTokens); thinking != nil {
		req.Thinking = thinking
	}
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
		inputSchema, err := normalizeToolSchemaChecked(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}
		req.Tools = append(req.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: inputSchema})
	}
	// Only the leading run of system messages may be hoisted into
	// req.System; later system messages (freshness stamp, reflection
	// checkpoints, ...) stay in place as <system-reminder> user turns
	// so the prompt prefix stays append-only for prompt caching.
	msgs = repairToolCallIDs(demoteMidConversationSystemMessages(msgs))
	var system []string
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			system = append(system, messageText(m))
		case RoleAssistant:
			blocks, err := anthropicAssistantBlocks(m)
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
	return json.Marshal(req)
}

func anthropicThinkingForModel(model string, maxTokens int) *anthropicThinking {
	effort := ReasoningEffortForModel(model)
	if effort == "" || effort == "none" {
		return nil
	}
	budget := reasoningBudgetTokens(effort, maxTokens)
	if budget <= 0 {
		return nil
	}
	return &anthropicThinking{Type: "enabled", BudgetTokens: budget}
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
				continue
			}
			if p.Image == nil {
				return nil, fmt.Errorf("image part with nil Image")
			}
			src := anthropicImageSource{}
			if p.Image.URL != "" && !strings.HasPrefix(p.Image.URL, "data:") {
				src = anthropicImageSource{Type: "url", URL: p.Image.URL}
			} else {
				data := p.Image.Data
				mediaType := p.Image.MediaType
				if data == "" && strings.HasPrefix(p.Image.URL, "data:") {
					mt, d, ok := parseDataURI(p.Image.URL)
					if ok {
						mediaType, data = mt, d
					}
				}
				if mediaType == "" || data == "" {
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
		if strings.TrimSpace(tc.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				return nil, fmt.Errorf("anthropic tool_use %s args: %w", tc.Name, err)
			}
		}
		blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
	}
	return blocks, nil
}
