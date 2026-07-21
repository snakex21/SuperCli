package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"

// NormalizeAnthropicBaseURL canonicalizes a user-supplied Anthropic base URL to
// the API version root (e.g. ".../v1"), so callers can safely append
// "/messages" or "/models" without producing a doubled path.
//
// Anthropic's documented endpoint is https://api.anthropic.com/v1/messages, and
// many Anthropic-compatible proxies advertise the full ".../v1/messages" URL.
// Users naturally paste that whole URL as the provider base URL. Without this
// normalization the request path becomes ".../v1/messages/messages" (404) and
// model discovery hits ".../v1/messages/v1/models" (404) — i.e. "the Anthropic
// endpoint doesn't work". Stripping a trailing "/messages" (and any trailing
// slash) makes both the paste-the-endpoint and paste-the-root forms work.
func NormalizeAnthropicBaseURL(base string) string {
	base = strings.TrimSpace(base)
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/messages") {
		base = strings.TrimSuffix(base, "/messages")
		base = strings.TrimRight(base, "/")
	}
	return base
}

// AnthropicConfig configures the native Anthropic Messages API provider.
type AnthropicConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
	// Timeout is the idle/inactivity timeout for the SSE stream (no data
	// from the server, also bounds time-to-first-token), NOT a whole-
	// request cap. Defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. Defaults to 30s.
	ConnectTimeout time.Duration
	HTTPClient     *http.Client
	Capabilities   *CapabilityRegistry
}

// AnthropicProvider implements Provider using Anthropic's /v1/messages SSE API.
type AnthropicProvider struct {
	cfg  AnthropicConfig
	http *http.Client
	caps *CapabilityRegistry
}

func NewAnthropic(cfg AnthropicConfig) (*AnthropicProvider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("llm.NewAnthropic: Model is empty")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	cfg.BaseURL = NormalizeAnthropicBaseURL(cfg.BaseURL)
	cfg.APIKey = CleanAPIKey(cfg.APIKey)
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second // idle/inactivity timeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
		transport.ResponseHeaderTimeout = 0                 // do NOT cap header wait: a slow local model may delay first byte
		cfg.HTTPClient = &http.Client{Transport: transport} // no Client.Timeout: streaming body must not be capped
	}
	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	return &AnthropicProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps}, nil
}

func (p *AnthropicProvider) Name() string { return p.cfg.Model }

func (p *AnthropicProvider) SupportsVision() bool {
	if _, ok := p.caps.Get(p.cfg.Model); ok {
		return p.caps.HasVision(p.cfg.Model)
	}
	return strings.Contains(strings.ToLower(p.cfg.Model), "claude")
}

func (p *AnthropicProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("Complete: no messages")
	}
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("Complete: message %d: %w", i, err)
		}
	}
	// Do not gate attachments on catalog metadata. Anthropic-compatible
	// gateways frequently expose custom model IDs whose capabilities are not
	// present in our registry; the provider response is the source of truth.
	body, err := buildAnthropicRequest(p.cfg.Model, msgs, tools, true, p.cfg.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	out := make(chan Delta, 16)
	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				select {
				case out <- Delta{Err: fmt.Errorf("provider panic: %v", r)}:
				default:
				}
			}
		}()
		// Derive a cancellable child context so the idle watchdog can
		// abort a stalled stream. cancel runs after the read completes.
		reqCtx, cancel := context.WithCancel(ctx)
		notify := func(msg string) {
			select {
			case out <- Delta{Notice: msg}:
			case <-ctx.Done():
			}
		}
		resp, err := p.do(reqCtx, body, notify)
		if err != nil {
			cancel()
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer cancel()
		respBody := newIdleTimeoutReader(resp.Body, p.cfg.Timeout, cancel)
		defer respBody.Close()
		p.streamSSE(ctx, respBody, out)
	}()
	return out, nil
}

// do posts the request with the same bounded 429/5xx retry policy as
// the OpenAI provider: up to maxAttempts total, Retry-After honoured
// (clamped by rateLimitWaitBudget), a notify callback per wait so the
// UI shows the rate-limit pause. notify may be nil.
func (p *AnthropicProvider) do(ctx context.Context, body []byte, notify func(string)) (*http.Response, error) {
	const maxAttempts = 3
	waitBudget := rateLimitWaitBudget
	thinkingRetried := false
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("anthropic-version", anthropicVersion)
		if p.cfg.APIKey != "" {
			req.Header.Set("x-api-key", p.cfg.APIKey)
		}
		resp, err := p.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if effort, ok := LearnReasoningEffortFromError(p.cfg.Model, resp.StatusCode, respBody); ok && !thinkingRetried {
			if patched, patchedOK := patchAnthropicThinking(body, effort); patchedOK {
				body = patched
				thinkingRetried = true
				continue
			}
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5
		if !retryable || attempt >= maxAttempts {
			return nil, fmt.Errorf("http %d: %s%s%s", resp.StatusCode, string(respBody), badRequestEffortHint(resp.StatusCode, respBody), rateLimitExhaustedHint(p.cfg.Model, resp.StatusCode))
		}
		backoff := retryWait(resp.Header, attempt, waitBudget)
		waitBudget -= backoff
		if notify != nil {
			notify(rateLimitNotice(p.cfg.Model, resp.StatusCode, backoff, attempt, maxAttempts))
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
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
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := anthropicRequest{Model: model, MaxTokens: maxTokens, Stream: true}
	if thinking := anthropicThinkingForModel(model, maxTokens); thinking != nil {
		req.Thinking = thinking
	}
	for _, t := range tools {
		req.Tools = append(req.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: normalizeToolSchema(t.Schema)})
	}
	// Only the leading run of system messages may be hoisted into
	// req.System; later system messages (freshness stamp, reflection
	// checkpoints, ...) stay in place as <system-reminder> user turns
	// so the prompt prefix stays append-only for prompt caching.
	msgs = demoteMidConversationSystemMessages(msgs)
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

func (p *AnthropicProvider) streamSSE(ctx context.Context, r io.Reader, out chan<- Delta) {
	emit := func(d Delta) bool {
		select {
		case out <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	toolAcc := make(map[int]*ToolCall)
	finishReason := ""
	var lastUsage *Usage
	reasoningOpen := false
	_ = parseSSE(r, func(eventName, data string) error {
		var ev anthropicEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			// Input-side accounting. Fold the cache read/creation
			// tokens into Input so it means "total prompt tokens"
			// like OpenAI's prompt_tokens, keeping the cache-hit
			// denominator consistent across providers; CachedInput
			// is the portion served from Anthropic's prompt cache.
			if u := ev.Message.Usage; u.InputTokens > 0 || u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
				in := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
				lastUsage = &Usage{Input: in, Total: in, CachedInput: u.CacheReadInputTokens}
			}
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				args := "{}"
				if len(ev.ContentBlock.Input) > 0 {
					args = string(ev.ContentBlock.Input)
				}
				toolAcc[ev.Index] = &ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name, Arguments: args}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				content := ev.Delta.Text
				if reasoningOpen {
					content = "</thinking>\n" + strings.TrimLeft(content, "\r\n")
					reasoningOpen = false
				}
				if content != "" && !emit(Delta{Content: content}) {
					return ctx.Err()
				}
			case "thinking_delta":
				content := ev.Delta.Thinking
				if !reasoningOpen {
					content = "<thinking>" + content
					reasoningOpen = true
				}
				if content != "" && !emit(Delta{Content: content}) {
					return ctx.Err()
				}
			case "input_json_delta":
				if tc := toolAcc[ev.Index]; tc != nil {
					if tc.Arguments == "{}" {
						tc.Arguments = ""
					}
					tc.Arguments += ev.Delta.PartialJSON
				}
			}
		case "content_block_stop":
			if tc := toolAcc[ev.Index]; tc != nil {
				tcCopy := *tc
				if !emit(Delta{ToolCall: &tcCopy}) {
					return ctx.Err()
				}
				delete(toolAcc, ev.Index)
			}
		case "message_delta":
			finishReason = anthropicFinishReason(ev.Delta.StopReason)
			if ev.Usage.OutputTokens > 0 {
				// Merge with the input-side usage captured at
				// message_start (nil when the server never sent one).
				if lastUsage == nil {
					lastUsage = &Usage{}
				}
				lastUsage.Output = ev.Usage.OutputTokens
				lastUsage.Total = lastUsage.Input + lastUsage.Output
			}
		case "message_stop":
			if reasoningOpen {
				if !emit(Delta{Content: "</thinking>"}) {
					return ctx.Err()
				}
				reasoningOpen = false
			}
			if finishReason == "" {
				finishReason = "stop"
			}
			if !emit(Delta{FinishReason: finishReason, Usage: lastUsage}) {
				return ctx.Err()
			}
		case "error":
			if ev.Error.Message != "" && !emit(Delta{Err: fmt.Errorf("anthropic: %s", ev.Error.Message)}) {
				return ctx.Err()
			}
		}
		return nil
	})
}

func anthropicFinishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return reason
	}
}

type anthropicEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	// Message carries the message_start payload; its usage holds the
	// input-side accounting (including the prompt-cache breakdown).
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicUsage is the usage object of the Messages API. Anthropic
// reports input_tokens EXCLUSIVE of cache activity: prompt tokens
// served from the cache arrive in cache_read_input_tokens and tokens
// written to it in cache_creation_input_tokens. All fields are simply
// zero when the API (or a proxy) omits them — missing telemetry must
// never break the cloud path.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func patchAnthropicThinking(body []byte, effort string) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if effort == "" || effort == "none" {
		delete(req, "thinking")
	} else {
		maxTokens := 4096
		if v, ok := req["max_tokens"].(float64); ok && v > 0 {
			maxTokens = int(v)
		}
		req["thinking"] = map[string]any{"type": "enabled", "budget_tokens": reasoningBudgetTokens(effort, maxTokens)}
	}
	out, err := json.Marshal(req)
	return out, err == nil
}
