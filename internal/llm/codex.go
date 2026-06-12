package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CodexTokenSource supplies ChatGPT-subscription credentials to
// the codex provider. Implemented by codexauth.Manager.
type CodexTokenSource interface {
	// Token returns a valid access token and ChatGPT account id.
	Token(ctx context.Context) (access, accountID string, err error)
	// Refresh forces a token refresh (after a 401) and returns
	// the new access token.
	Refresh(ctx context.Context) (string, error)
}

// CodexConfig configures the ChatGPT-backend ("Codex") provider.
//
// Unlike api.openai.com, the ChatGPT backend speaks the
// *Responses API* (POST <backend>/responses, SSE stream), not
// chat completions — the same endpoint the Codex CLI uses. This
// provider contains a minimal translation layer from SuperCli's
// chat-completions-shaped Message/ToolDef types to Responses API
// input items and back.
type CodexConfig struct {
	// BackendURL is the ChatGPT backend root, e.g.
	// "https://chatgpt.com/backend-api/codex" (no trailing slash).
	BackendURL string
	// Model is the model id, e.g. "gpt-5-codex". Required.
	Model string
	// Tokens supplies access tokens. Required.
	Tokens CodexTokenSource
	// Timeout caps each HTTP request (default 120s — Codex
	// streams can be long-lived).
	Timeout time.Duration
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
}

// CodexProvider is the Provider implementation backed by a
// ChatGPT subscription instead of an API key.
type CodexProvider struct {
	cfg  CodexConfig
	http *http.Client
	caps *CapabilityRegistry
}

// NewCodex builds a CodexProvider.
func NewCodex(cfg CodexConfig) (*CodexProvider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("llm.NewCodex: Model is empty")
	}
	if cfg.Tokens == nil {
		return nil, fmt.Errorf("llm.NewCodex: Tokens (token source) is nil — run /login first")
	}
	if cfg.BackendURL == "" {
		cfg.BackendURL = "https://chatgpt.com/backend-api/codex"
	}
	cfg.BackendURL = strings.TrimRight(cfg.BackendURL, "/")
	if cfg.HTTPClient == nil {
		if cfg.Timeout <= 0 {
			cfg.Timeout = 120 * time.Second
		}
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	return &CodexProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps}, nil
}

// Name implements Provider.
func (p *CodexProvider) Name() string { return p.cfg.Model }

// SupportsVision reports vision capability from the registry;
// gpt-5 family models handle images, so default to true when
// the registry has no entry.
func (p *CodexProvider) SupportsVision() bool {
	if _, ok := p.caps.Get(p.cfg.Model); ok {
		return p.caps.HasVision(p.cfg.Model)
	}
	return true
}

// Complete implements Provider by streaming from the Responses
// API endpoint. On a 401 the token is refreshed once and the
// request retried.
func (p *CodexProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
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
	reqBody, err := buildCodexRequest(p.cfg.Model, msgs, tools, p.SupportsVision())
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
		resp, err := p.doWithAuth(ctx, reqBody)
		if err != nil {
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer resp.Body.Close()
		p.streamCodexSSE(ctx, resp.Body, out)
	}()
	return out, nil
}

// doWithAuth performs the POST with bearer + ChatGPT headers,
// refreshing the token and retrying once on 401.
func (p *CodexProvider) doWithAuth(ctx context.Context, body []byte) (*http.Response, error) {
	access, accountID, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			p.cfg.BackendURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("originator", "codex_cli_go")
		if accountID != "" {
			req.Header.Set("chatgpt-account-id", accountID)
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
		if resp.StatusCode == http.StatusUnauthorized && attempt == 1 {
			// Transparent refresh + single retry.
			access, err = p.cfg.Tokens.Refresh(ctx)
			if err != nil {
				return nil, fmt.Errorf("codex auth expired and refresh failed: %w", err)
			}
			continue
		}
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}
}

// streamCodexSSE parses Responses API SSE events into Deltas.
func (p *CodexProvider) streamCodexSSE(ctx context.Context, r io.Reader, out chan<- Delta) {
	emit := func(d Delta) bool {
		select {
		case out <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var usage *Usage
	reasoningOpen := false
	sentRole := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || isDone(data) {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if !sentRole {
				if !emit(Delta{Role: RoleAssistant}) {
					return
				}
				sentRole = true
			}
			content := ev.Delta
			if reasoningOpen {
				content = "</thinking>\n" + strings.TrimLeft(content, "\r\n")
				reasoningOpen = false
			}
			if !emit(Delta{Content: content}) {
				return
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			content := ev.Delta
			if !reasoningOpen {
				content = "<thinking>" + content
				reasoningOpen = true
			}
			if !emit(Delta{Content: content}) {
				return
			}
		case "response.output_item.done":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				if !emit(Delta{ToolCall: &ToolCall{
					ID:        ev.Item.CallID,
					Name:      ev.Item.Name,
					Arguments: ev.Item.Arguments,
				}}) {
					return
				}
			}
		case "response.completed":
			if ev.Response != nil && ev.Response.Usage != nil {
				usage = &Usage{
					Input:  ev.Response.Usage.InputTokens,
					Output: ev.Response.Usage.OutputTokens,
					Total:  ev.Response.Usage.TotalTokens,
				}
			}
			if reasoningOpen {
				if !emit(Delta{Content: "</thinking>"}) {
					return
				}
				reasoningOpen = false
			}
			d := Delta{FinishReason: "stop"}
			if usage != nil {
				d.Usage = usage
			}
			emit(d)
			return
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			emit(Delta{Err: fmt.Errorf("codex: %s", msg)})
			return
		}
	}
	if err := scanner.Err(); err != nil {
		emit(Delta{Err: fmt.Errorf("sse: %w", err)})
	}
}

// --- SSE event shape ---

type codexEvent struct {
	Type     string         `json:"type"`
	Delta    string         `json:"delta,omitempty"`
	Item     *codexItemEv   `json:"item,omitempty"`
	Response *codexRespMeta `json:"response,omitempty"`
}

type codexItemEv struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type codexRespMeta struct {
	Usage *codexUsage `json:"usage,omitempty"`
	Error *codexError `json:"error,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// --- request translation: chat-completions shape → Responses API ---

type codexRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions,omitempty"`
	Input             []codexItem     `json:"input"`
	Tools             []codexToolDecl `json:"tools,omitempty"`
	ToolChoice        string          `json:"tool_choice"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Store             bool            `json:"store"`
	Stream            bool            `json:"stream"`
	Include           []string        `json:"include"`
	// Reasoning carries the effort level the same way the Codex
	// CLI does ({"effort": "...", "summary": "auto"}). Omitted
	// when no effort is configured.
	Reasoning *codexReasoning `json:"reasoning,omitempty"`
}

// codexReasoning is the Responses API reasoning config.
type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexItem struct {
	Type string `json:"type"`
	// message items
	Role    string             `json:"role,omitempty"`
	Content []codexContentPart `json:"content,omitempty"`
	// function_call items
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	// function_call_output items
	Output string `json:"output,omitempty"`
}

type codexContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type codexToolDecl struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// buildCodexRequest translates SuperCli's chat-completion-shaped
// history into Responses API items:
//
//   - system messages    → the top-level "instructions" field
//   - user/assistant     → {"type":"message","content":[input_text|output_text]}
//   - assistant ToolCall → {"type":"function_call",...}
//   - tool results       → {"type":"function_call_output",...}
func buildCodexRequest(model string, msgs []Message, tools []ToolDef, vision bool) ([]byte, error) {
	req := codexRequest{
		Model:      model,
		ToolChoice: "auto",
		Store:      false,
		Stream:     true,
		Include:    []string{},
	}
	// The ChatGPT backend rejects "none"; the Codex CLI never
	// sends it either — skip the field in that case.
	if e := ReasoningEffort(); e != "" && e != "none" && SupportsReasoningEffort(model) {
		req.Reasoning = &codexReasoning{Effort: e, Summary: "auto"}
	}
	for _, t := range tools {
		req.Tools = append(req.Tools, codexToolDecl{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  normalizeToolSchema(t.Schema),
		})
	}
	var instructions []string
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			instructions = append(instructions, messageText(m))
		case RoleTool:
			req.Input = append(req.Input, codexItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		case RoleAssistant:
			if text := messageText(m); text != "" {
				req.Input = append(req.Input, codexItem{
					Type: "message", Role: "assistant",
					Content: []codexContentPart{{Type: "output_text", Text: text}},
				})
			}
			for _, tc := range m.ToolCalls {
				req.Input = append(req.Input, codexItem{
					Type:      "function_call",
					Name:      tc.Name,
					Arguments: tc.Arguments,
					CallID:    tc.ID,
				})
			}
		default: // user
			parts, err := codexUserParts(m, vision)
			if err != nil {
				return nil, err
			}
			req.Input = append(req.Input, codexItem{
				Type: "message", Role: "user", Content: parts,
			})
		}
	}
	req.Instructions = strings.Join(instructions, "\n\n")
	return json.Marshal(req)
}

// messageText flattens a message's text content.
func messageText(m Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// codexUserParts encodes a user message as input_text/input_image
// content parts. Images are dropped when vision is off.
func codexUserParts(m Message, vision bool) ([]codexContentPart, error) {
	if len(m.Parts) == 0 {
		return []codexContentPart{{Type: "input_text", Text: m.Content}}, nil
	}
	out := make([]codexContentPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText:
			out = append(out, codexContentPart{Type: "input_text", Text: p.Text})
		case PartTypeImage:
			if !vision {
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
			out = append(out, codexContentPart{Type: "input_image", ImageURL: url})
		default:
			return nil, fmt.Errorf("unknown part type %q", p.Type)
		}
	}
	if len(out) == 0 {
		out = append(out, codexContentPart{Type: "input_text", Text: m.Content})
	}
	return out, nil
}
