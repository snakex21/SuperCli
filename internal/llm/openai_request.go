package llm

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type openCodeSessionCtxKey struct{}

// The fallback covers requests that do not belong to a persisted conversation,
// such as model discovery and diagnostics. It is created once per process and
// reused, so adding the required header never adds I/O or per-request ID work.
// Shape matches what Zen's free-tier edge accepts: ses_ + 26 lowercase hex.
var openCodeProcessSessionID = zenSessionFromEntropy(fmt.Sprintf("proc-%d", time.Now().UnixNano()))

// openCodeZenUserAgent mirrors `opencode/1.18.32`'s exact UA from the
// successful mitm capture (post_2.json / post_3.json, 2026-09-22).
const openCodeZenUserAgent = "opencode/1.18.32 ai-sdk/provider-utils/4.0.40 runtime/bun/1.3.14"

// WithOpenCodeSession attaches the stable SuperCli conversation ID used by
// OpenCode Go for routing and prompt caching. Other providers ignore it.
func WithOpenCodeSession(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if ctx == nil || sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, openCodeSessionCtxKey{}, sessionID)
}

func openCodeSessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(openCodeSessionCtxKey{}).(string)
	return strings.TrimSpace(sessionID)
}

// isZenSessionWireID reports whether s already matches the free-tier wire
// shape: "ses_" + exactly 26 lowercase hex characters (bisect 2026-09-22:
// any other length/charset → 403 FreeTierError).
func isZenSessionWireID(s string) bool {
	if !strings.HasPrefix(s, "ses_") || len(s) != 4+26 {
		return false
	}
	for _, c := range s[4:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// zenSessionFromEntropy maps arbitrary entropy to a stable ses_+26hex ID.
func zenSessionFromEntropy(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "ses_" + hex.EncodeToString(sum[:13])
}

// normalizeZenSessionID rewrites any SuperCli/process session ID into the
// wire shape Zen accepts. Already-valid IDs pass through unchanged so a
// genuine capture session keeps its identity. Deterministic: the same
// conversation always yields the same prompt_cache_key across turns.
func normalizeZenSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = openCodeProcessSessionID
	}
	if isZenSessionWireID(sessionID) {
		return sessionID
	}
	return zenSessionFromEntropy(sessionID)
}

// IsOpenCodeZenBaseURL reports whether baseURL points at an OpenCode Zen
// free-tier endpoint. Exported so the factory can route by catalog transport
// (or the OpenAI-chat default for unknown models) without a per-model switch.
func IsOpenCodeZenBaseURL(baseURL string) bool {
	return isOpenCodeZenBaseURL(baseURL)
}

func isOpenCodeZenBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(u.Hostname(), "opencode.ai") {
		return false
	}
	path := strings.TrimRight(strings.ToLower(u.EscapedPath()), "/")
	return path == "/zen/v1" || strings.HasPrefix(path, "/zen/v1/") ||
		path == "/zen/go/v1" || strings.HasPrefix(path, "/zen/go/v1/")
}

// ensureOpenCodeZenGateToolDefs appends the free-tier gate pair (bash, read)
// when absent, in the nested OpenAI function shape Zen expects on
// /chat/completions. Either tool alone → 403; the pair opens any
// openai-compatible free model. Idempotent. SuperCli never advertises these
// names as its own tools — the agent rewrites returned bash/read calls onto
// SuperCli's real tools (rewritePlaceholderToolCall).
func ensureOpenCodeZenGateToolDefs(tools []ToolDef) []ToolDef {
	var hasBash, hasRead bool
	for _, t := range tools {
		switch t.Name {
		case "bash":
			hasBash = true
		case "read":
			hasRead = true
		}
	}
	if !hasBash {
		tools = append(tools, ToolDef{
			Name:        "bash",
			Description: "run a command",
			Schema:      `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`,
		})
	}
	if !hasRead {
		tools = append(tools, ToolDef{
			Name:        "read",
			Description: "Read a file",
			Schema:      `{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`,
		})
	}
	return tools
}

// ApplyOpenCodeZenHeaders applies the exact public-client headers the real
// OpenCode CLI sends to Zen (see packages/opencode/src/session/llm/request.ts
// and effect/runtime-flags.ts). Zen's edge rejects Go's default User-Agent
// with Cloudflare error 1010, and the client/UA markers keep SuperCli
// indistinguishable from opencode itself. Other endpoints are left untouched.
func ApplyOpenCodeZenHeaders(req *http.Request, baseURL string) {
	if req == nil || !isOpenCodeZenBaseURL(baseURL) {
		return
	}
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer public")
	}
	// Flag.OPENCODE_CLIENT defaults to "cli" in official OpenCode.
	req.Header.Set("X-OpenCode-Client", "cli")
	// Real client UA is richer than bare version: `opencode/<ver> ai-sdk/...
	// runtime/bun/<ver>` (re-captured from CLI 1.18.32 via mitmproxy 2026-09-22).
	// Free-tier edge parses the leading `opencode/<semver>` for the 1.18.0 gate;
	// the trailing ai-sdk/runtime bumps must track the binary we impersonate.
	req.Header.Set("User-Agent", openCodeZenUserAgent)
	// Captured real client always sends x-opencode-project (defaults to "global"
	// when FLAG OPENCODE_PROJECT is unset).
	if req.Header.Get("X-OpenCode-Project") == "" {
		req.Header.Set("X-OpenCode-Project", "global")
	}
	// Genuine CLI never sends Accept: text/event-stream on /responses — the
	// capture shows `Accept: */*` with stream:true in the body instead.
	req.Header.Set("Accept", "*/*")
	// Capture lists Accept-Encoding: gzip, deflate, br, zstd, but setting it
	// here would disable Go's transparent gzip decode (error bodies and any
	// compressed stream would arrive raw). The free-tier gate does not check
	// AE — leave it to the Transport default (gzip + auto-inflate).
	if req.Header.Get("Connection") == "" {
		req.Header.Set("Connection", "keep-alive")
	}
	sessionID := normalizeZenSessionID(openCodeSessionFromContext(req.Context()))
	req.Header.Set("X-OpenCode-Session", sessionID)
	// x-opencode-request carries the user message ID (msg_ ascending form)
	// so Zen can correlate a single prompt. Generate one per request.
	req.Header.Set("X-OpenCode-Request", newOpenCodeMessageID())
}

// newOpenCodeMessageID mimics Identifier.ascending("message") from
// packages/opencode/src/id/id.ts: prefix "msg", 6-byte big-endian
// (ms<<12|counter) timestamp, then 14 random base62 chars.
func newOpenCodeMessageID() string {
	const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	now := uint64(time.Now().UnixMilli())<<12 + uint64(randUint32()&0xfff)
	var ts [6]byte
	for i := 5; i >= 0; i-- {
		ts[i] = byte(now)
		now >>= 8
	}
	var rnd [14]byte
	_, _ = rand.Read(rnd[:])
	var b strings.Builder
	b.WriteString("msg_")
	const hex = "0123456789abcdef"
	for _, c := range ts {
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	for _, c := range rnd {
		b.WriteByte(base62[int(c)%62])
	}
	return b.String()
}

func randUint32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

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
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	// Sampling parameters. Pointers + omitempty: a parameter the user
	// never set is absent from the body, so the server applies its own
	// default exactly as it did before pass-through existed. They sit
	// ahead of Messages deliberately — they are constant for a whole
	// session, so they can never shift the volatile part of the body,
	// and the KV-cached prompt prefix is built from messages/tools
	// anyway.
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	Seed             *int64   `json:"seed,omitempty"`
	// llama.cpp/LM Studio extensions; only emitted for local hosts.
	TopK          *int           `json:"top_k,omitempty"`
	MinP          *float64       `json:"min_p,omitempty"`
	RepeatPenalty *float64       `json:"repeat_penalty,omitempty"`
	Messages      []openaiReqMsg `json:"messages"`
	Stream        bool           `json:"stream"`
	// StreamOptions asks the server to emit a final usage chunk in
	// streaming mode. Required by the OpenAI spec (and LM Studio,
	// vLLM, etc.) to get prompt/completion token counts back when
	// stream=true — without it usage is silently empty. Pointer +
	// omitempty so it is dropped entirely for non-streaming calls,
	// which some endpoints reject if the field is present.
	StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
	Tools         []openaiToolDecl     `json:"tools,omitempty"`
	// ParallelToolCalls advertises that SuperCli can accept more than one
	// independent tool call in a single assistant response. The agent runtime
	// already executes read-only batches concurrently; omitting this hint left
	// some OpenAI-compatible gateways/models emitting one read per provider
	// round. Pointer + omitempty keeps plain chat requests unchanged.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
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
	Effort  string `json:"effort"`
	Exclude *bool  `json:"exclude,omitempty"`
}

func visibleOpenAIReasoning(effort string) *openAIReasoning {
	exclude := false
	return &openAIReasoning{Effort: effort, Exclude: &exclude}
}

// openaiStreamOptions carries the include_usage flag.
type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiReqMsg struct {
	Role             string             `json:"role"`
	Content          any                `json:"content,omitempty"`
	Name             string             `json:"name,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
	ToolCalls        []openaiReqToolRef `json:"tool_calls,omitempty"`
	ReasoningContent *string            `json:"reasoning_content,omitempty"`
	Reasoning        *string            `json:"reasoning,omitempty"`
	ReasoningText    *string            `json:"reasoning_text,omitempty"`
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
	return buildOpenAIRequestWithReasoningKey(model, model, msgs, tools, vision, cachePrompt, format, 0, Sampling{})
}

func buildOpenAIRequestWithReasoningKey(model, supportKey string, msgs []Message, tools []ToolDef, vision bool, cachePrompt bool, format openAIReasoningFormat, maxTokens int, sampling Sampling) ([]byte, error) {
	msgs = repairToolCallIDs(demoteMidConversationSystemMessages(msgs))
	req := openaiRequest{
		Model:            model,
		MaxTokens:        maxTokens,
		Temperature:      sampling.Temperature,
		TopP:             sampling.TopP,
		PresencePenalty:  sampling.PresencePenalty,
		FrequencyPenalty: sampling.FrequencyPenalty,
		Seed:             sampling.Seed,
		TopK:             sampling.TopK,
		MinP:             sampling.MinP,
		RepeatPenalty:    sampling.RepeatPenalty,
		Stream:           true,
		StreamOptions:    &openaiStreamOptions{IncludeUsage: true},
		CachePrompt:      cachePrompt,
	}
	if e := ReasoningEffortForModelWithCapability(supportKey, format != openAIReasoningNone); e != "" {
		if format == openAIReasoningUnified {
			// OpenRouter/Kilo-style gateways can return the model's exposed
			// reasoning stream, but some default to hiding it unless exclude is
			// explicitly false. SuperCli has native <thinking> rendering, so ask
			// the gateway to preserve provider-exposed reasoning for the UI.
			req.Reasoning = visibleOpenAIReasoning(e)
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
	if len(req.Tools) > 0 {
		enabled := true
		req.ParallelToolCalls = &enabled
	}
	for _, m := range msgs {
		rm := openaiReqMsg{
			Role:       string(m.Role),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		applyChatReasoning(&rm, m, model)
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
				Function: openaiToolFn{Name: tc.Name, Arguments: providerSafeToolArguments(tc.Arguments)},
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
			req["reasoning"] = map[string]any{"effort": effort, "exclude": false}
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
	// Local Chat Completions uses the standard flat control. LM Studio and
	// llama.cpp can silently ignore a gateway's nested reasoning object.
	// CapabilityModel marks the Opencode gateway wrapper, including local ones.
	if isLocalBaseURL(p.cfg.BaseURL) && p.cfg.CapabilityModel == "" {
		return openAIReasoningEffort
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

const imageInputOmittedPlaceholder = "[image omitted: current model has no image input]"

func messagesContainImage(msgs []Message) bool {
	for _, m := range msgs {
		if m.HasImage() {
			return true
		}
	}
	return false
}

func encodeOpenAIContent(m Message, vision bool) (any, error) {
	// Most local OpenAI-compatible servers (LM Studio, Ollama,
	// llama.cpp) are happiest with plain string content for
	// text-only messages. Use multipart arrays only when an image
	// actually needs to be sent.
	if len(m.Parts) == 0 {
		return m.Content, nil
	}
	var first string
	size := 0
	for _, p := range m.Parts {
		var text string
		switch p.Type {
		case PartTypeText:
			text = p.Text
		case PartTypeImage:
			if vision {
				return encodeOpenAIParts(m, vision)
			}
			text = imageInputOmittedPlaceholder
		default:
			continue
		}
		// Preserve historical separators exactly: leading empty text is
		// ignored, but an empty part after visible text adds a newline.
		if size > 0 {
			size++
		} else {
			first = text
		}
		size += len(text)
	}
	if size == len(first) {
		return first, nil
	}
	var b strings.Builder
	b.Grow(size)
	for _, p := range m.Parts {
		var text string
		switch p.Type {
		case PartTypeText:
			text = p.Text
		case PartTypeImage:
			text = imageInputOmittedPlaceholder
		default:
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String(), nil
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
				out = append(out, openaiPart{Type: "text", Text: imageInputOmittedPlaceholder})
				continue
			}
			url, err := resolveImageURL(p.Image)
			if err != nil {
				return nil, err
			}
			out = append(out, openaiPart{
				Type:     "image_url",
				ImageURL: &openaiImgURL{URL: url},
			})
		case PartTypeReasoning:
			// Serialized separately as native assistant reasoning.
		default:
			return nil, fmt.Errorf("unknown part type %q", p.Type)
		}
	}
	return out, nil
}

// isImageRejection reports whether a non-2xx response indicates the
// upstream refused the image parts in the request body (e.g. a text-only
// model behind an OpenAI-compatible gateway, which serde-style parsers
// reject with "unknown variant `image_url`, expected `text`"). Such a
// request can be retried once with the images stripped; the rejection is
// also learned so later turns skip the doomed attempt entirely.
//
// The match is deliberately narrow: only unambiguous rejection language
// counts. A 400 that merely mentions image_url for another reason (a
// malformed or oversized attachment, a bad media type) must NOT mark a
// vision-capable model as text-only.
func isImageRejection(status int, body []byte) bool {
	switch status {
	case 400, 415, 422:
	default:
		return false
	}
	low := strings.ToLower(string(body))
	// serde-style parsers reject the whole body schema: the image part
	// variant does not exist for this model.
	if strings.Contains(low, "unknown variant") && strings.Contains(low, "image_url") {
		return true
	}
	// Some OpenAI-compatible aggregators wrap the upstream response and use
	// this wording instead of explicitly calling the model "text-only":
	// "Model only supports text input; received unsupported content type
	// 'image_url'." Both clauses are required so an ordinary bad image URL or
	// media-type error cannot incorrectly disable vision for a capable model.
	if strings.Contains(low, "only supports text input") &&
		strings.Contains(low, "unsupported content type") &&
		strings.Contains(low, "image_url") {
		return true
	}
	// Gateway language for text-only models. Keep this deliberately
	// specific: "unsupported image format" or "unsupported media type"
	// describes a bad attachment, not a blind model.
	return (strings.Contains(low, "image") || strings.Contains(low, "vision") || strings.Contains(low, "multimodal")) && (strings.Contains(low, "image input is not supported") ||
		strings.Contains(low, "image inputs are not supported") ||
		strings.Contains(low, "images are not supported by this model") ||
		strings.Contains(low, "model does not support image") ||
		strings.Contains(low, "model doesn't support image") ||
		strings.Contains(low, "model does not support vision") ||
		strings.Contains(low, "model doesn't support vision") ||
		strings.Contains(low, "model does not support multimodal") ||
		strings.Contains(low, "text-only model"))
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
