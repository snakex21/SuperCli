package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	// Timeout is the idle/inactivity timeout for the SSE stream: the
	// maximum gap with no data from the server (also bounds time-to-
	// first-token). It is NOT a whole-request deadline, so a slow but
	// alive stream is never cut. If zero, defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. If zero,
	// defaults to 30s.
	ConnectTimeout time.Duration
	// HTTPClient overrides the default http.Client. If nil, a client
	// with a dial-only timeout (no whole-request cap) is used.
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
	// CachePrompt overrides KV-prompt-cache hinting. When nil (the
	// default) the provider auto-detects: requests to local/private
	// hosts (localhost, loopback, RFC-1918, link-local, unspecified)
	// get `"cache_prompt": true` so llama.cpp-family servers reuse
	// the KV cache across turns; public endpoints (api.openai.com,
	// OpenRouter, ...) never see the field, because cloud OpenAI
	// rejects unknown fields with HTTP 400. Set explicitly to force
	// the hint on (e.g. a llama.cpp box on a public IP) or off.
	CachePrompt *bool
}

// OpenAIProvider is the production Provider for the OpenAI-compat
// streaming chat-completions API.
type OpenAIProvider struct {
	cfg  OpenAIConfig
	http *http.Client
	caps *CapabilityRegistry
	// cachePrompt: resolved from cfg.CachePrompt (explicit) or
	// isLocalBaseURL (auto). See OpenAIConfig.CachePrompt.
	cachePrompt bool
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
	// Resolution order for the cache_prompt hint: an explicit
	// per-construction CachePrompt wins; else the process-global
	// default set from config.toml `cache_prompt` (nil = unset); else
	// auto-detect by whether the backend is local.
	cachePrompt := isLocalBaseURL(cfg.BaseURL)
	if d := cachePromptDefaultVal(); d != nil {
		cachePrompt = *d
	}
	if cfg.CachePrompt != nil {
		cachePrompt = *cfg.CachePrompt
	}
	return &OpenAIProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps, cachePrompt: cachePrompt}, nil
}

// isLocalBaseURL reports whether baseURL points at a local or
// private-network server (llama.cpp, LM Studio, Ollama, vLLM on the
// LAN...). Only such hosts get llama.cpp-specific request fields like
// cache_prompt; public/cloud endpoints must never see them (OpenAI
// returns HTTP 400 on unknown fields).
//
// Note on slots: we deliberately do NOT pin id_slot. llama.cpp already
// auto-selects the slot with the longest common prefix for each
// request, which is exactly the per-session KV reuse we want; pinning
// a slot id would make parallel agents (coordinator + workers sharing
// one server) evict each other's cache.
// IsLocalBaseURL is the exported form of isLocalBaseURL, for callers
// outside this package (e.g. deciding Darwin's parallel/sequential mode
// by whether the active backend is local).
func IsLocalBaseURL(baseURL string) bool { return isLocalBaseURL(baseURL) }

func isLocalBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
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

	reqBody, err := buildOpenAIRequest(p.cfg.Model, msgs, tools, hasVision, p.cachePrompt)
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

		// HTTP request with bounded retry: 429 and 5xx
		// responses are retried up to maxAttempts total. The wait
		// honours the Retry-After header when present (seconds or
		// HTTP-date), falling back to exponential backoff (0.5s,
		// 1s); total sleep is capped by rateLimitWaitBudget. Each
		// retry emits a Delta.Notice so the UI shows the wait
		// instead of appearing hung. Other statuses and transport
		// errors fail immediately.
		url := p.cfg.BaseURL + "/chat/completions"
		const maxAttempts = 3
		waitBudget := rateLimitWaitBudget
		var resp *http.Response
		// streamCancel cancels the request context of the attempt that
		// actually proceeds to streaming. It is invoked after the read
		// completes (or by the idle watchdog if the stream stalls). Each
		// attempt builds its own cancellable child; non-streaming attempts
		// cancel immediately so no context leaks across the retry loop.
		var streamCancel func()
		effortRetried := false
		for attempt := 1; ; attempt++ {
			reqCtx, cancel := context.WithCancel(ctx)
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(reqBody))
			if err != nil {
				cancel()
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

			resp, err = p.http.Do(req)
			if err != nil {
				cancel()
				select {
				case out <- Delta{Err: fmt.Errorf("http: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			if resp.StatusCode/100 == 2 {
				streamCancel = cancel
				break
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			cancel()
			if effort, ok := LearnReasoningEffortFromError(p.cfg.Model, resp.StatusCode, body); ok && !effortRetried {
				if patched, patchedOK := patchOpenAIReasoningEffort(reqBody, effort); patchedOK {
					reqBody = patched
					effortRetried = true
					continue
				}
			}
			retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5
			if !retryable || attempt >= maxAttempts {
				select {
				case out <- Delta{Err: fmt.Errorf("http %d: %s%s%s", resp.StatusCode, string(body), badRequestEffortHint(resp.StatusCode, body), rateLimitExhaustedHint(p.cfg.Model, resp.StatusCode))}:
				case <-ctx.Done():
				}
				return
			}
			backoff := retryWait(resp.Header, attempt, waitBudget)
			waitBudget -= backoff
			select {
			case out <- Delta{Notice: rateLimitNotice(p.cfg.Model, resp.StatusCode, backoff, attempt, maxAttempts)}:
			case <-ctx.Done():
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
		}
		// Cancel the streaming request context once the read finishes,
		// and wrap the body with an idle watchdog that fires cancel if no
		// data arrives within the idle timeout.
		defer streamCancel()
		body := newIdleTimeoutReader(resp.Body, p.cfg.Timeout, streamCancel)
		defer body.Close()

		// Stream parse. We intentionally parse line-by-line like
		// agent-go instead of waiting for a blank-line terminated SSE
		// event. Some local OpenAI-compatible servers (LM Studio,
		// llama.cpp variants) emit `data: {...}\n` chunks without a
		// following empty line; waiting for blank lines makes the TUI
		// appear stuck on "working".
		toolAcc := make(map[int]*ToolCall)
		var lastUsage *Usage
		var lastTimings *llamaTimings
		reasoningOpen := false
		parseErr := parseOpenAIDataLines(body, func(data string) error {
			var chunk openaiChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Skip non-JSON payloads (e.g. ping frames).
				return nil
			}
			var raw openaiRawChunk
			_ = json.Unmarshal([]byte(data), &raw)
			// llama.cpp attaches timings to its chunks; remember the
			// latest so the cache-miss derivation below works whether
			// timings ride the usage chunk itself or an earlier one.
			if chunk.Timings != nil {
				lastTimings = chunk.Timings
				deriveCachedFromTimings(lastUsage, lastTimings)
			}
			if chunk.Usage != nil {
				lastUsage = &Usage{Input: chunk.Usage.PromptTokens, Output: chunk.Usage.CompletionTokens, Total: chunk.Usage.TotalTokens}
				if d := chunk.Usage.PromptTokensDetails; d != nil {
					lastUsage.CachedInput = d.CachedTokens
				}
				if d := chunk.Usage.CompletionTokensDetails; d != nil {
					lastUsage.Reasoning = d.ReasoningTokens
				}
				// Older llama.cpp builds report the cached split only
				// via timings, not prompt_tokens_details.
				deriveCachedFromTimings(lastUsage, lastTimings)
				// Servers that honour stream_options.include_usage
				// (LM Studio, vLLM, OpenAI) send the usage in a FINAL
				// chunk whose choices array is EMPTY. The per-choice
				// emit below would miss it, so surface it here as a
				// standalone usage delta. Without this, streamed runs
				// always report 0 tokens.
				if len(chunk.Choices) == 0 {
					select {
					case out <- Delta{Usage: lastUsage}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			for i, choice := range chunk.Choices {
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
				reasoning := choice.Delta.ReasoningContent
				if reasoning == "" && i < len(raw.Choices) {
					reasoning = extractReasoningText(raw.Choices[i].Delta)
				}
				if reasoning != "" {
					content := reasoning
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
	Model    string         `json:"model"`
	Messages []openaiReqMsg `json:"messages"`
	Stream   bool           `json:"stream"`
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
	// CachePrompt asks llama.cpp-family servers to reuse the KV
	// cache for the common prompt prefix across requests. Gated:
	// only emitted for local/private BaseURLs (or an explicit
	// config override) — cloud OpenAI 400s on unknown fields.
	// omitempty drops it entirely when false.
	CachePrompt bool `json:"cache_prompt,omitempty"`
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
	msgs = demoteMidConversationSystemMessages(msgs)
	req := openaiRequest{
		Model:         model,
		Stream:        true,
		StreamOptions: &openaiStreamOptions{IncludeUsage: true},
		CachePrompt:   cachePrompt,
	}
	if e := ReasoningEffortForModel(model); e != "" {
		req.ReasoningEffort = e
	}
	for _, t := range tools {
		req.Tools = append(req.Tools, openaiToolDecl{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  normalizeToolSchema(t.Schema),
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
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if effort == "" {
		delete(req, "reasoning_effort")
	} else {
		req["reasoning_effort"] = effort
	}
	out, err := json.Marshal(req)
	return out, err == nil
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
	Timings *llamaTimings `json:"timings,omitempty"`
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
type llamaTimings struct {
	PromptN    int `json:"prompt_n"`
	CacheN     int `json:"cache_n"`
	PredictedN int `json:"predicted_n"`
}

// deriveCachedFromTimings fills Usage.CachedInput from llama.cpp's
// timings block when the OpenAI-style prompt_tokens_details breakdown
// is absent (older llama.cpp builds). Preference order: an explicit
// cache_n; else prompt_tokens - prompt_n (everything the server did
// not re-evaluate came from the KV cache). No-op when the usage
// already carries a cached count or there is nothing to derive, so
// cloud responses without timings are untouched.
func deriveCachedFromTimings(u *Usage, t *llamaTimings) {
	if u == nil || t == nil || u.CachedInput > 0 {
		return
	}
	cached := t.CacheN
	if cached == 0 && t.PromptN > 0 && u.Input > t.PromptN {
		cached = u.Input - t.PromptN
	}
	if cached > u.Input {
		cached = u.Input
	}
	if cached > 0 {
		u.CachedInput = cached
	}
}

type openaiRawChunk struct {
	Choices []struct {
		Delta map[string]json.RawMessage `json:"delta"`
	} `json:"choices"`
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

func extractReasoningText(delta map[string]json.RawMessage) string {
	if len(delta) == 0 {
		return ""
	}
	var parts []string
	for key, raw := range delta {
		if !isReasoningJSONKey(key) {
			continue
		}
		if s := extractStringLeaves(raw); strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "")
}

func isReasoningJSONKey(key string) bool {
	k := strings.ToLower(key)
	if strings.Contains(k, "finish") || strings.Contains(k, "token") || strings.Contains(k, "usage") {
		return false
	}
	return strings.Contains(k, "reasoning") || strings.Contains(k, "thinking") || strings.Contains(k, "thought")
}

func extractStringLeaves(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var out []string
		for _, item := range arr {
			if v := extractStringLeaves(item); v != "" {
				out = append(out, v)
			}
		}
		return strings.Join(out, "")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		preferred := []string{"text", "content", "value", "delta", "thinking", "reasoning"}
		var out []string
		seen := make(map[string]bool)
		for _, key := range preferred {
			if v, ok := obj[key]; ok {
				seen[key] = true
				if s := extractStringLeaves(v); s != "" {
					out = append(out, s)
				}
			}
		}
		for key, v := range obj {
			if seen[key] {
				continue
			}
			if s := extractStringLeaves(v); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, "")
	}
	return ""
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
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *openaiPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *openaiCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// openaiPromptTokensDetails carries the cached-prompt breakdown that
// OpenAI and llama.cpp/LM Studio report inside usage. cached_tokens is
// the portion of prompt_tokens the backend served from its KV cache.
type openaiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// openaiCompletionTokensDetails carries the reasoning-token breakdown
// that reasoning models report inside usage. reasoning_tokens counts the
// hidden chain-of-thought tokens billed as completion tokens.
type openaiCompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
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
