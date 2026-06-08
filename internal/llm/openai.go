package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIConfig configures an OpenAI-compat Provider. Any provider
// that speaks the /v1/chat/completions SSE protocol can use this:
// OpenAI, Azure OpenAI, Together, Groq, OpenRouter, llama.cpp's
// server, LM Studio, etc.
type OpenAIConfig struct {
	// BaseURL is the provider root without trailing slash.
	// Default: "https://api.openai.com/v1".
	BaseURL string
	// APIKey is the bearer token. Optional — local providers
	// (LM Studio, Ollama) don't need one.
	APIKey string
	// Model is the model id, e.g. "gpt-4o-mini". Required.
	Model string
	// Timeout caps the whole HTTP request. If zero, defaults to 60s.
	Timeout time.Duration
	// HTTPClient overrides the default http.Client. If nil, a
	// client with Timeout (or 60s) is used.
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
}

// OpenAIProvider is the production Provider for the OpenAI-compat
// streaming chat-completions API.
type OpenAIProvider struct {
	cfg  OpenAIConfig
	http *http.Client
	caps *CapabilityRegistry
}

// NewOpenAI returns an OpenAIProvider. BaseURL defaults to the
// public OpenAI endpoint. Model is required. APIKey is optional
// (local providers like LM Studio and Ollama don't need one).
func NewOpenAI(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("llm.NewOpenAI: Model is empty")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	cfg.APIKey = CleanAPIKey(cfg.APIKey)
	if cfg.HTTPClient == nil {
		if cfg.Timeout <= 0 {
			cfg.Timeout = 60 * time.Second
		}
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	return &OpenAIProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps}, nil
}

// Name implements Provider. Returns the configured model id.
func (p *OpenAIProvider) Name() string { return p.cfg.Model }

// SupportsVision returns true when the model is known to handle
// image inputs.
func (p *OpenAIProvider) SupportsVision() bool {
	return p.caps.HasVision(p.cfg.Model)
}

// Complete implements Provider.
func (p *OpenAIProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
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

	// Vision gating: if the model cannot see images, strip them
	// with a warning on the channel's first delta (rather than
	// failing the request). The agent loop decides how to react.
	hasVision := p.SupportsVision()
	warnedNoVision := false

	reqBody, err := buildOpenAIRequest(p.cfg.Model, msgs, tools, hasVision)
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
		// Emit an early warning if any image part was dropped.
		if !hasVision {
			for _, m := range msgs {
				if m.HasImage() {
					warnedNoVision = true
					break
				}
			}
			if warnedNoVision {
				select {
				case out <- Delta{
					Err: fmt.Errorf("llm.OpenAI: model %q does not support vision; image parts were dropped", p.cfg.Model),
				}:
				case <-ctx.Done():
				}
				return
			}
		}

		// HTTP request.
		url := p.cfg.BaseURL + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			select {
			case out <- Delta{Err: fmt.Errorf("build request: %w", err)}:
			case <-ctx.Done():
			}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if key := CleanAPIKey(p.cfg.APIKey); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := p.http.Do(req)
		if err != nil {
			select {
			case out <- Delta{Err: fmt.Errorf("http: %w", err)}:
			case <-ctx.Done():
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode/100 != 2 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			select {
			case out <- Delta{Err: fmt.Errorf("http %d: %s", resp.StatusCode, string(body))}:
			case <-ctx.Done():
			}
			return
		}

		// Stream parse. We intentionally parse line-by-line like
		// agent-go instead of waiting for a blank-line terminated SSE
		// event. Some local OpenAI-compatible servers (LM Studio,
		// llama.cpp variants) emit `data: {...}\n` chunks without a
		// following empty line; waiting for blank lines makes the TUI
		// appear stuck on "working".
		toolAcc := make(map[int]*ToolCall)
		var lastUsage *Usage
		reasoningOpen := false
		parseErr := parseOpenAIDataLines(resp.Body, func(data string) error {
			var chunk openaiChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Skip non-JSON payloads (e.g. ping frames).
				return nil
			}
			if chunk.Usage != nil {
				lastUsage = &Usage{Input: chunk.Usage.PromptTokens, Output: chunk.Usage.CompletionTokens, Total: chunk.Usage.TotalTokens}
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Role != "" {
					select {
					case out <- Delta{Role: Role(choice.Delta.Role)}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if choice.Delta.Content != "" {
					content := choice.Delta.Content
					if reasoningOpen {
						content = "</thinking>\n" + strings.TrimLeft(content, "\r\n")
						reasoningOpen = false
					}
					select {
					case out <- Delta{Content: content}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if choice.Delta.ReasoningContent != "" {
					content := choice.Delta.ReasoningContent
					if !reasoningOpen {
						content = "<thinking>" + content
						reasoningOpen = true
					}
					select {
					case out <- Delta{Content: content}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					acc, exists := toolAcc[tc.Index]
					if !exists {
						acc = &ToolCall{ID: tc.ID, Name: tc.Function.Name}
						toolAcc[tc.Index] = acc
					}
					if tc.Function.Name != "" {
						acc.Name = tc.Function.Name
					}
					if tc.ID != "" {
						acc.ID = tc.ID
					}
					acc.Arguments += tc.Function.Arguments
				}
				if choice.FinishReason != "" {
					// Flush accumulated tool calls BEFORE the
					// terminal delta so consumers see them.
					for i := 0; i < len(toolAcc); i++ {
						if tc, ok := toolAcc[i]; ok {
							tcCopy := *tc
							select {
							case out <- Delta{ToolCall: &tcCopy}:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
					}
					d := Delta{FinishReason: choice.FinishReason}
					if lastUsage != nil {
						d.Usage = lastUsage
					}
					select {
					case out <- d:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return nil
		})
		if parseErr != nil {
			select {
			case out <- Delta{Err: fmt.Errorf("sse: %w", parseErr)}:
			case <-ctx.Done():
			}
		} else if reasoningOpen {
			select {
			case out <- Delta{Content: "</thinking>"}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

func parseOpenAIDataLines(r io.Reader, onData func(data string) error) error {
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
			return nil
		}
		if data == "" {
			continue
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// --- request body ---

type openaiRequest struct {
	Model    string           `json:"model"`
	Messages []openaiReqMsg   `json:"messages"`
	Stream   bool             `json:"stream"`
	Tools    []openaiToolDecl `json:"tools,omitempty"`
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

func buildOpenAIRequest(model string, msgs []Message, tools []ToolDef, vision bool) ([]byte, error) {
	req := openaiRequest{Model: model, Stream: true}
	for _, t := range tools {
		req.Tools = append(req.Tools, openaiToolDecl{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  json.RawMessage(t.Schema),
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
}

type openaiChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type openaiDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolRef `json:"tool_calls,omitempty"`
}

type openaiToolRef struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function openaiToolFn `json:"function"`
}

type openaiToolFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// EncodeBase64 is a small helper used by callers that need to
// turn a file into a base64 string for an ImageRef. It is a
// convenience around encoding/base64.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Ensure bufio is referenced (used in streaming helpers that may
// land here in a follow-up).
var _ = bufio.NewReader
