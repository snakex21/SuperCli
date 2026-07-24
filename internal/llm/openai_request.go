package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func parseOpenAIDataLines(r io.Reader, onData func(data string) error) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if isDone(data) {
			return true, nil
		}
		if data == "" {
			continue
		}
		if err := onData(data); err != nil {
			return false, err
		}
	}
	return false, scanner.Err()
}

// --- request body ---

type openaiRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens,omitempty"`
	Messages  []openaiReqMsg `json:"messages"`
	Stream    bool           `json:"stream"`
	// StreamOptions asks the server to emit a final usage chunk in
	// streaming mode. Required by the OpenAI spec (and LM Studio,
	// vLLM, etc.) to get prompt/completion token counts back when
	// stream=true — without it usage is silently empty. Pointer +
	// omitempty so it is dropped entirely for non-streaming calls,
	// which some endpoints reject if the field is present.
	StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
	Tools         []openaiToolDecl     `json:"tools,omitempty"`
	// ReasoningEffort is only set for models known to support
	// it (see SupportsReasoningEffort); other models never see
	// the field, so non-OpenAI endpoints cannot reject it.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Reasoning is the unified OpenRouter-compatible control used for models
	// discovered as reasoning-capable behind a gateway.
	Reasoning *openAIReasoning `json:"reasoning,omitempty"`
	// CachePrompt asks llama.cpp-family servers to reuse the KV
	// cache for the common prompt prefix across requests. Gated:
	// only emitted for local/private BaseURLs (or an explicit
	// config override) — cloud OpenAI 400s on unknown fields.
	// omitempty drops it entirely when false.
	CachePrompt bool `json:"cache_prompt,omitempty"`
}

type openAIReasoning struct {
	Effort string `json:"effort"`
}

// openaiStreamOptions carries the include_usage flag.
type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiReqMsg struct {
	Role       string             `json:"role"`
	Content    any                `json:"content,omitempty"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []openaiReqToolRef `json:"tool_calls,omitempty"`
}

type openaiReqToolRef struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function openaiToolFn `json:"function"`
}

type openaiPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *openaiImgURL `json:"image_url,omitempty"`
}

type openaiImgURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type openaiToolDecl struct {
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // embedded JSON object, NOT a string
}

func buildOpenAIRequest(model string, msgs []Message, tools []ToolDef, vision bool, cachePrompt bool) ([]byte, error) {
	format := openAIReasoningNone
	if SupportsReasoningEffort(model) {
		format = openAIReasoningEffort
	}
	return buildOpenAIRequestWithReasoning(model, msgs, tools, vision, cachePrompt, format)
}

func buildOpenAIRequestWithReasoning(model string, msgs []Message, tools []ToolDef, vision bool, cachePrompt bool, format openAIReasoningFormat) ([]byte, error) {
	return buildOpenAIRequestWithReasoningKey(model, model, msgs, tools, vision, cachePrompt, format, 0)
}

func buildOpenAIRequestWithReasoningKey(model, supportKey string, msgs []Message, tools []ToolDef, vision bool, cachePrompt bool, format openAIReasoningFormat, maxTokens int) ([]byte, error) {
	msgs = demoteMidConversationSystemMessages(msgs)
	req := openaiRequest{
		Model:         model,
		MaxTokens:     maxTokens,
		Stream:        true,
		StreamOptions: &openaiStreamOptions{IncludeUsage: true},
		CachePrompt:   cachePrompt,
	}
	if e := ReasoningEffortForModelWithCapability(supportKey, format != openAIReasoningNone); e != "" {
		if format == openAIReasoningUnified {
			req.Reasoning = &openAIReasoning{Effort: e}
		} else if format == openAIReasoningEffort {
			req.ReasoningEffort = e
		}
	}
	for _, t := range tools {
		parameters, err := normalizeOpenAIToolSchemaChecked(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}
		req.Tools = append(req.Tools, openaiToolDecl{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  parameters,
			},
		})
	}
	for _, m := range msgs {
		rm := openaiReqMsg{
			Role:       string(m.Role),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		// Tool result messages carry plain string content; OpenAI
		// expects {"role":"tool","tool_call_id":"...","content":"..."}.
		if m.Role == RoleTool {
			rm.Content = m.Content
		} else {
			content, err := encodeOpenAIContent(m, vision)
			if err != nil {
				return nil, err
			}
			rm.Content = content
		}
		// Assistant tool calls.
		for _, tc := range m.ToolCalls {
			rm.ToolCalls = append(rm.ToolCalls, openaiReqToolRef{
				ID:       tc.ID,
				Type:     "function",
				Function: openaiToolFn{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		req.Messages = append(req.Messages, rm)
	}
	return json.Marshal(req)
}

func patchOpenAIReasoningEffort(body []byte, effort string) ([]byte, bool) {
	return patchOpenAIReasoning(body, effort, openAIReasoningEffort)
}

func patchOpenAIReasoning(body []byte, effort string, format openAIReasoningFormat) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if format == openAIReasoningUnified {
		delete(req, "reasoning_effort")
		if effort == "" {
			delete(req, "reasoning")
		} else {
			req["reasoning"] = map[string]any{"effort": effort}
		}
	} else {
		delete(req, "reasoning")
		if effort == "" {
			delete(req, "reasoning_effort")
		} else {
			req["reasoning_effort"] = effort
		}
	}
	out, err := json.Marshal(req)
	return out, err == nil
}

func (p *OpenAIProvider) reasoningFormat() openAIReasoningFormat {
	gateway := IsUnifiedReasoningGateway(p.cfg.BaseURL)
	// Offer best-effort reasoning for every OpenAI-compatible model. Known
	// OpenAI families use reasoning_effort; gateways and unknown families use
	// the portable reasoning:{effort} object. A rejecting backend is learned
	// once and the retry removes the parameter.
	capability := true
	if !SupportsReasoningEffortWithCapability(p.reasoningKey(), capability) {
		return openAIReasoningNone
	}
	if gateway {
		return openAIReasoningUnified
	}
	// Unknown reasoning families discovered from provider metadata generally
	// sit behind normalizing gateways, where the unified object is portable.
	if capability && !supportsReasoningEffortByName(p.cfg.Model) {
		return openAIReasoningUnified
	}
	return openAIReasoningEffort
}

// IsUnifiedReasoningGateway reports whether an OpenAI-compatible endpoint
// accepts the cross-provider reasoning:{effort:...} control. These gateways
// perform their own per-model mapping, so the control can remain available even
// when their lightweight /models response omitted capability metadata.
func IsUnifiedReasoningGateway(baseURL string) bool {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(base, "openrouter") ||
		strings.Contains(base, "api.kilo.ai") ||
		strings.Contains(base, "opencode.ai/zen")
}

func (p *OpenAIProvider) reasoningKey() string {
	return ReasoningSupportKey(p.cfg.BaseURL, p.cfg.Model)
}

func (p *OpenAIProvider) hasReasoningCapability() bool {
	if p.caps == nil {
		return false
	}
	model := p.cfg.CapabilityModel
	if model == "" {
		model = p.cfg.Model
	}
	return p.caps.HasReasoning(model)
}

func encodeOpenAIContent(m Message, vision bool) (any, error) {
	// Most local OpenAI-compatible servers (LM Studio, Ollama,
	// llama.cpp) are happiest with plain string content for
	// text-only messages. Use multipart arrays only when an image
	// actually needs to be sent.
	if len(m.Parts) == 0 {
		return m.Content, nil
	}
	hasImage := false
	for _, p := range m.Parts {
		if p.Type == PartTypeImage && vision {
			hasImage = true
			break
		}
	}
	if !hasImage {
		var b strings.Builder
		for _, p := range m.Parts {
			if p.Type == PartTypeText {
				b.WriteString(p.Text)
			}
		}
		return b.String(), nil
	}
	return encodeOpenAIParts(m, vision)
}

func encodeOpenAIParts(m Message, vision bool) ([]openaiPart, error) {
	// Legacy text-only path: if Parts is empty, encode Content as
	// a single text part.
	if len(m.Parts) == 0 {
		return []openaiPart{{Type: "text", Text: m.Content}}, nil
	}
	out := make([]openaiPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText:
			out = append(out, openaiPart{Type: "text", Text: p.Text})
		case PartTypeImage:
			if !vision {
				// Drop with a no-op part. The warning is sent
				// at the channel level in Complete().
				continue
			}
			img := p.Image
			if img == nil {
				return nil, fmt.Errorf("image part with nil Image")
			}
			url := img.URL
			if url == "" {
				if img.MediaType == "" || img.Data == "" {
					return nil, fmt.Errorf("image part: incomplete (need URL or MediaType+Data)")
				}
				url = "data:" + img.MediaType + ";base64," + img.Data
			}
			out = append(out, openaiPart{
				Type:     "image_url",
				ImageURL: &openaiImgURL{URL: url},
			})
		default:
			return nil, fmt.Errorf("unknown part type %q", p.Type)
		}
	}
	return out, nil
}

// --- response chunk ---

type openaiChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
	// Timings is llama.cpp-specific: the server attaches its
	// performance block to /v1/chat/completions responses. Absent on
	// cloud backends; ignored when nil.
	Timings *llamaTimings   `json:"timings,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func openAIErrorMessage(raw json.RawMessage) string {
	var object struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	}
	if json.Unmarshal(raw, &object) == nil {
		if object.Message != "" {
			return object.Message
		}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return text
	}
	return string(raw)
}

// llamaTimings is the llama.cpp server performance block, used here
// for cache-miss telemetry: prompt_n is the number of prompt tokens
// the server actually (re-)evaluated this request, cache_n (newer
// builds) is the number of prompt tokens reused from the KV cache,
// predicted_n is the generated-token count. The native /completion
// endpoint reports tokens_evaluated/tokens_cached instead, but
// SuperCli only speaks /v1/chat/completions, where the timings form
// (plus, on newer builds, usage.prompt_tokens_details) is what
// actually arrives.
