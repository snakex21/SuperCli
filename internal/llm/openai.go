package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
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
	// MaxTokens caps generated tokens. Zero leaves the provider default.
	// This is the OpenAI-compatible chat-completions `max_tokens` field.
	MaxTokens int
	// Timeout bounds both the wait for HTTP response headers and the maximum
	// idle gap between SSE bytes. It is not a whole-request deadline, so a
	// slow but active stream is never cut. If zero, defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. If zero,
	// defaults to 30s.
	ConnectTimeout time.Duration
	// HTTPClient overrides the default http.Client. If nil, a client
	// with a dial-only timeout (no whole-request cap) is used.
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
	// CapabilityModel overrides the registry ID used for capability checks.
	// Opencode transports send a raw model ID while keeping a namespaced
	// opencode/<id> entry in the shared registry.
	CapabilityModel string
	// CachePrompt overrides KV-prompt-cache hinting. When nil (the
	// default) the provider auto-detects: requests to local/private
	// hosts (localhost, loopback, RFC-1918, link-local, unspecified)
	// get `"cache_prompt": true` so llama.cpp-family servers reuse
	// the KV cache across turns; public endpoints (api.openai.com,
	// OpenRouter, ...) never see the field, because cloud OpenAI
	// rejects unknown fields with HTTP 400. Set explicitly to force
	// the hint on (e.g. a llama.cpp box on a public IP) or off.
	CachePrompt *bool
	// Sampling overrides the process-global sampling settings for this
	// one provider. The zero value (no field set) falls back to
	// SamplingDefault(), which is where config.toml `[sampling]` lands.
	// Nothing set anywhere means no sampling fields are sent at all.
	Sampling Sampling
}

// OpenAIProvider is the production Provider for the OpenAI-compat
// streaming chat-completions API.
type OpenAIProvider struct {
	cfg  OpenAIConfig
	http *http.Client
	caps *CapabilityRegistry
	// imageRejected is transport-local evidence that this exact provider
	// instance rejected OpenAI image parts. Keep it out of the shared
	// capability registry: a broken/mismatched gateway must not globally
	// mark the same model id as text-only for other providers.
	imageRejected atomic.Bool
	// cachePrompt: resolved from cfg.CachePrompt (explicit) or
	// isLocalBaseURL (auto). See OpenAIConfig.CachePrompt.
	cachePrompt bool
	// sampling: resolved once, already stripped of the llama.cpp-only
	// extensions when the backend is not local.
	sampling Sampling
}

type openAIReasoningFormat uint8

const (
	openAIReasoningNone openAIReasoningFormat = iota
	// Direct OpenAI chat completions use the top-level reasoning_effort field.
	openAIReasoningEffort
	// OpenRouter and compatible gateways normalize models through
	// reasoning: {effort: ...}.
	openAIReasoningUnified
)

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
	cfg.BaseURL = ResolveOpenAIEndpoints(cfg.BaseURL).BaseURL
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
		transport.ResponseHeaderTimeout = 0                 // bounded per request below so custom clients behave the same
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
	// Sampling: explicit per-construction settings win, else the
	// process-global default from config.toml. top_k/min_p/repeat_penalty
	// are llama.cpp-family extensions and are dropped for non-local
	// backends, which reject unknown fields with HTTP 400.
	sampling := resolveSampling(cfg.Sampling)
	if !isLocalBaseURL(cfg.BaseURL) {
		sampling = sampling.withoutLocalExtensions()
	}
	logSampling("openai", cfg.Model, sampling)
	return &OpenAIProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps, cachePrompt: cachePrompt, sampling: sampling}, nil
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

// SupportsVision reports whether this configured model is safe to try with
// image input. A provider-discovered/configured model whose modalities are
// still unknown remains optimistic; a completely absent model stays false
// here so capability UIs do not claim support out of thin air. Complete is
// slightly more permissive and also tries a completely unknown model.
func (p *OpenAIProvider) SupportsVision() bool {
	model := p.capabilityModel()
	if _, ok := p.caps.Get(model); !ok {
		return false
	}
	return p.caps.AllowsVisionAttempt(model)
}

// capabilityModel returns the model id used as the capability-registry
// key: an explicit CapabilityModel when set (opencode gateway wrappers
// register the prefixed id), else the wire model id.
func (p *OpenAIProvider) capabilityModel() string {
	model := strings.TrimSpace(p.cfg.CapabilityModel)
	if model == "" {
		model = p.cfg.Model
	}
	return model
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

	reasoningFormat := p.reasoningFormat()
	// Known text-only metadata blocks image parts before the request leaves
	// SuperCli. Unknown capability metadata is tried optimistically; if the
	// upstream then rejects image input, the transport-local learning below
	// retries once without images and skips the doomed attempt on later turns.
	visionAttempt := p.caps.AllowsVisionAttempt(p.capabilityModel()) && !p.imageRejected.Load()
	reqBody, err := buildOpenAIRequestWithReasoningKey(p.cfg.Model, p.reasoningKey(), msgs, tools, visionAttempt, p.cachePrompt, reasoningFormat, p.cfg.MaxTokens, p.sampling)
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
		// HTTP request with bounded retry: 429 and 5xx
		// responses are retried with a status-specific bounded attempt count. The wait
		// honours the Retry-After header when present (seconds or
		// HTTP-date), falling back to exponential backoff (0.5s,
		// 1s); total sleep is capped by rateLimitWaitBudget. Each
		// retry emits a Delta.Notice so the UI shows the wait
		// instead of appearing hung. Other statuses and transport
		// errors fail immediately.
		url := ResolveOpenAIEndpoints(p.cfg.BaseURL).ChatCompletions
		waitBudget := rateLimitWaitBudget
		rateAttempts := 0
		var resp *http.Response
		// streamCancel cancels the request context of the attempt that
		// actually proceeds to streaming. It is invoked after the read
		// completes (or by the idle watchdog if the stream stalls). Each
		// attempt builds its own cancellable child; non-streaming attempts
		// cancel immediately so no context leaks across the retry loop.
		var streamCancel func()
		effortRetried := false
		visionRetried := false
		for {
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
			ApplyOpenCodeZenHeaders(req, p.cfg.BaseURL)

			resp, err = doWithResponseHeaderTimeout(p.http, req, p.cfg.Timeout, cancel)
			if err != nil {
				cancel()
				select {
				case out <- Delta{Err: fmt.Errorf("http: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			// The server accepted this HTTP request, so it may bill
			// quota for it regardless of the status code below.
			// Transport-level failures above never reached the
			// provider and correctly stay uncounted.
			noteProviderRequest(p.cfg.BaseURL)
			if resp.StatusCode/100 == 2 {
				streamCancel = cancel
				break
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			cancel()
			if effort, ok := LearnReasoningEffortFromError(p.reasoningKey(), resp.StatusCode, body); ok && !effortRetried {
				if patched, patchedOK := patchOpenAIReasoning(reqBody, effort, reasoningFormat); patchedOK {
					reqBody = patched
					effortRetried = true
					continue
				}
			}
			// The upstream refused the image parts (text-only model behind
			// an OpenAI-compatible gateway). Learn the rejection so later
			// turns skip images entirely, rebuild the request without them
			// and retry once — the conversation survives instead of dying
			// on a 400.
			if visionAttempt && !visionRetried && messagesContainImage(msgs) && isImageRejection(resp.StatusCode, body) {
				p.imageRejected.Store(true)
				reqBody, err = buildOpenAIRequestWithReasoningKey(p.cfg.Model, p.reasoningKey(), msgs, tools, false, p.cachePrompt, reasoningFormat, p.cfg.MaxTokens, p.sampling)
				if err == nil {
					visionRetried = true
					select {
					case out <- Delta{Notice: fmt.Sprintf("model %q rejected image input; retrying without images", p.cfg.Model)}:
					case <-ctx.Done():
						return
					}
					continue
				}
			}
			responseErr := newHTTPResponseError(resp.StatusCode, body,
				providerErrorHint(p.cfg.BaseURL, p.cfg.Model, resp.StatusCode, body), resp.Header)
			if !isRetryableProviderResponse(resp.StatusCode, body) {
				select {
				case out <- Delta{Err: responseErr}:
				case <-ctx.Done():
				}
				return
			}
			// A long, explicit 429 Retry-After is a quota window, not a
			// transient burst. Sleeping only our ten-second retry budget and
			// asking the same backend again cannot succeed; surface it now so
			// an explicitly configured fallback can take over.
			if retryAfter, ok := parseRetryAfter(resp.Header); resp.StatusCode == http.StatusTooManyRequests && ok && retryAfter > waitBudget {
				select {
				case out <- Delta{Err: responseErr}:
				case <-ctx.Done():
				}
				return
			}
			rateAttempts++
			maxAttempts := providerRetryAttempts(resp.StatusCode)
			if rateAttempts >= maxAttempts {
				select {
				case out <- Delta{Err: responseErr}:
				case <-ctx.Done():
				}
				return
			}
			backoff := retryWait(resp.Header, rateAttempts, waitBudget)
			waitBudget -= backoff
			select {
			case out <- Delta{Notice: rateLimitNotice(p.cfg.Model, resp.StatusCode, backoff, rateAttempts, maxAttempts)}:
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
		sawResponse := false
		sawPayload := false
		emittedFinish := false
		emit := func(d Delta) error {
			select {
			case out <- d:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		flushToolCalls := func() error {
			indices := make([]int, 0, len(toolAcc))
			for index := range toolAcc {
				indices = append(indices, index)
			}
			sort.Ints(indices)
			for _, index := range indices {
				tcCopy := *toolAcc[index]
				if err := emit(Delta{ToolCall: &tcCopy}); err != nil {
					return err
				}
			}
			toolAcc = make(map[int]*ToolCall)
			return nil
		}
		sawDone, parseErr := parseOpenAIDataLines(body, func(data string) error {
			if isSSEHeartbeatData(data) {
				return nil
			}
			var chunk openaiChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				sample := data
				if len(sample) > 256 {
					sample = sample[:256] + "..."
				}
				return fmt.Errorf("%w: %v (%q)", errSSEUnexpected, err, sample)
			}
			if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
				return fmt.Errorf("provider stream error: %s", openAIErrorMessage(chunk.Error))
			}
			if len(chunk.Choices) > 0 || chunk.Usage != nil || chunk.ID != "" || chunk.Object != "" {
				sawResponse = true
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
				reasoning := choice.Delta.ReasoningContent
				if reasoning == "" && i < len(raw.Choices) {
					reasoning = extractReasoningText(raw.Choices[i].Delta)
				}
				if reasoning != "" {
					sawPayload = true
					select {
					case out <- Delta{Reasoning: reasoning}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if choice.Delta.Content != "" {
					sawPayload = true
					select {
					case out <- Delta{Content: choice.Delta.Content}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					sawPayload = true
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
				if choice.FinishReason != "" && !emittedFinish {
					// Flush accumulated tool calls BEFORE the
					// terminal delta so consumers see them. The
					// accumulator is then RESET: some servers (newer
					// llama.cpp among them) send a second
					// finish_reason chunk (e.g. the final usage
					// frame), and re-flushing would emit every tool
					// call twice — the agent loop would then really
					// execute each call twice.
					if err := flushToolCalls(); err != nil {
						return err
					}
					d := Delta{FinishReason: choice.FinishReason}
					if lastUsage != nil {
						d.Usage = lastUsage
					}
					if err := emit(d); err != nil {
						return err
					}
					emittedFinish = true
				}
			}
			return nil
		})
		if parseErr != nil {
			select {
			case out <- Delta{Err: fmt.Errorf("sse: %w", parseErr)}:
			case <-ctx.Done():
			}
		} else if !sawResponse {
			_ = emit(Delta{Err: fmt.Errorf("sse: empty stream")})
		} else if !emittedFinish {
			// A number of OpenAI-compatible gateways close a valid SSE stream
			// cleanly without sending either [DONE] or a finish_reason chunk.
			// Treat that as a normal EOF only when we actually received model
			// payload. Read/transport failures still arrive as parseErr above,
			// while metadata-only streams remain errors instead of being silently
			// accepted as an empty assistant answer. Crucially, flush accumulated
			// tool calls here so ask_user and other tools are not lost just because
			// the gateway omitted its terminal marker.
			if !sawDone && !sawPayload {
				_ = emit(Delta{Err: fmt.Errorf("sse: stream ended before a terminal event")})
				return
			}
			hadTools := len(toolAcc) > 0
			if err := flushToolCalls(); err != nil {
				return
			}
			reason := "stop"
			if hadTools {
				reason = "tool_calls"
			}
			_ = emit(Delta{FinishReason: reason, Usage: lastUsage})
		}
	}()
	return out, nil
}
