package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	// Timeout is the idle/inactivity timeout for the SSE stream (no data
	// from the server, also bounds time-to-first-token), NOT a whole-
	// request cap — Codex streams can be long-lived. Defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. Defaults to 30s.
	ConnectTimeout time.Duration
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
	// DataDir is the resolved SuperCli data directory. When set, the
	// last rate-limit snapshot is persisted there (codex_ratelimits.json)
	// and reloaded at startup so the HUD `limit:` tile shows the most
	// recently known usage immediately — before the first /responses
	// call — without ever issuing an extra network request. Empty
	// disables persistence (the tile then appears only after the
	// first response, as before).
	DataDir string
	// AccountID scopes the persisted rate-limit snapshot to a
	// specific account, so multi-account setups keep one snapshot
	// file per account instead of sharing one (which made every
	// account display another account's usage). Empty uses the
	// legacy shared file. Pass the account id known at build time
	// (e.g. from the auth manager) — it is not required to match
	// the live token; it only namespaces the on-disk snapshot.
	AccountID string
}

// CodexProvider is the Provider implementation backed by a
// ChatGPT subscription instead of an API key.
type CodexProvider struct {
	cfg  CodexConfig
	http *http.Client
	caps *CapabilityRegistry

	// rl holds the most recent rate-limit snapshot parsed from the
	// HTTP response headers of /responses. The ChatGPT backend
	// returns usage percentages on every 200, so this is refreshed
	// on each stream without an extra request. Guarded by rlMu
	// because doWithAuth runs on the streaming goroutine while the
	// TUI reads it from the render goroutine.
	rlMu sync.Mutex
	rl   CodexRateLimits
}

// CodexRateLimits is a snapshot of the ChatGPT-subscription usage
// limits, parsed from the X-Codex-* headers the backend attaches to
// every /responses 200. Percentages are 0..100. OK is false when no
// usable headers were present (e.g. a non-Codex provider), in which
// case the HUD shows nothing.
type CodexRateLimits struct {
	// Primary is the short rolling window (typically 5h / 300 min).
	PrimaryUsedPct    int
	PrimaryWindowMin  int
	PrimaryResetAt    int64 // unix epoch seconds, 0 if unknown
	PrimaryResetAfter int64 // seconds until reset, 0 if unknown
	// Secondary is the long window (typically weekly / 10080 min).
	SecondaryUsedPct    int
	SecondaryWindowMin  int
	SecondaryResetAt    int64
	SecondaryResetAfter int64
	// OK reports whether at least one usage percentage was present.
	OK bool
}

// RateLimits returns the latest rate-limit snapshot. The bool is
// false until the first 200 carrying X-Codex-* headers arrives.
func (p *CodexProvider) RateLimits() (CodexRateLimits, bool) {
	p.rlMu.Lock()
	defer p.rlMu.Unlock()
	return p.rl, p.rl.OK
}

// setRateLimits stores a freshly parsed snapshot when it is usable
// and persists it to disk (best-effort) so the next process start can
// render the HUD tile immediately. A non-OK parse (no headers) is
// ignored so a stray non-Codex response can't wipe a previously good
// snapshot. accountID scopes the persisted snapshot so one account's
// usage is never shown under another.
//
// Persistence is best-effort and deliberately off the hot path: a
// failed write is silently ignored (it only delays the tile by one
// response and never affects the live stream).
func (p *CodexProvider) setRateLimits(accountID string, rl CodexRateLimits) {
	if !rl.OK {
		return
	}
	p.rlMu.Lock()
	p.rl = rl
	p.rlMu.Unlock()
	_ = saveCodexRateLimits(p.cfg.DataDir, accountID, rl)
}

// parseCodexRateLimits extracts the rate-limit snapshot from the
// X-Codex-* response headers. used-percent is parsed tolerantly
// (int or float, e.g. "1" or "1.5"); empty/garbage values are
// skipped rather than failing. OK is set when at least one of the
// two used-percent headers was present and parseable.
func parseCodexRateLimits(h http.Header) CodexRateLimits {
	var rl CodexRateLimits
	if pct, ok := parsePercent(h.Get("X-Codex-Primary-Used-Percent")); ok {
		rl.PrimaryUsedPct = pct
		rl.OK = true
	}
	rl.PrimaryWindowMin = parseIntHeader(h.Get("X-Codex-Primary-Window-Minutes"))
	rl.PrimaryResetAt = parseInt64Header(h.Get("X-Codex-Primary-Reset-At"))
	rl.PrimaryResetAfter = parseInt64Header(h.Get("X-Codex-Primary-Reset-After-Seconds"))

	if pct, ok := parsePercent(h.Get("X-Codex-Secondary-Used-Percent")); ok {
		rl.SecondaryUsedPct = pct
		rl.OK = true
	}
	rl.SecondaryWindowMin = parseIntHeader(h.Get("X-Codex-Secondary-Window-Minutes"))
	rl.SecondaryResetAt = parseInt64Header(h.Get("X-Codex-Secondary-Reset-At"))
	rl.SecondaryResetAfter = parseInt64Header(h.Get("X-Codex-Secondary-Reset-After-Seconds"))
	return rl
}

// parsePercent parses a used-percent header. Accepts ints ("11") and
// floats ("11.4", rounded down) and clamps to 0..100. Returns ok=false
// for empty or unparseable input.
func parsePercent(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if i, err := strconv.Atoi(s); err == nil {
		return clampPct(i), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return clampPct(int(f)), true
	}
	return 0, false
}

func clampPct(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func parseIntHeader(s string) int {
	if i, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return i
	}
	return 0
}

func parseInt64Header(s string) int64 {
	if i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return i
	}
	return 0
}

// FormatHUD renders the compact status-bar tile, e.g. "5h 1% · 7d 11%".
// When a reset-time for the shorter window is known it is appended to
// the primary segment, e.g. "5h 1% (4h32m) · 7d 11%". Returns "" when
// the snapshot is empty (OK=false) so callers can skip the tile.
//
// FormatHUD is a thin wrapper over formatHUDAt(time.Now()); the latter
// is the testable seam that takes the clock explicitly.
func (rl CodexRateLimits) FormatHUD() string {
	return rl.formatHUDAt(time.Now())
}

// formatHUDAt renders the tile as of now. It is reset-aware: a snapshot
// reloaded from disk may describe a window that has already rolled over
// (now >= ResetAt), in which case the stored used-percent is stale and
// the window is effectively back to ~0%. Each window is evaluated
// independently — the primary may have reset while the secondary has
// not, or vice versa — without ever issuing a network request.
//
// A reset window is shown with a tilde ("~0%") because the value is
// inferred, not yet confirmed by a real response. Its time annotation,
// when computable, counts down to the NEXT reset (ResetAt + window) so
// it never displays a past/negative duration.
func (rl CodexRateLimits) formatHUDAt(now time.Time) string {
	if !rl.OK {
		return ""
	}
	primaryPct, primaryReset := effectiveUsedPct(rl.PrimaryUsedPct, rl.PrimaryResetAt, now)
	primary := fmt.Sprintf("%s %s", windowLabel(rl.PrimaryWindowMin, "5h"), formatPct(primaryPct, primaryReset))
	if d := rl.primaryResetDuration(now, primaryReset); d > 0 {
		primary += " (" + shortDuration(d) + ")"
	}
	secondaryPct, secondaryReset := effectiveUsedPct(rl.SecondaryUsedPct, rl.SecondaryResetAt, now)
	secondary := fmt.Sprintf("%s %s", windowLabel(rl.SecondaryWindowMin, "7d"), formatPct(secondaryPct, secondaryReset))
	return primary + " · " + secondary
}

// effectiveUsedPct computes the used-percent to display for a single
// window as of now. When the window's reset time is known and has
// already passed (resetAt > 0 && now >= resetAt), the window has rolled
// over: the stored percent is stale, so it returns (0, true). Otherwise
// it returns the stored percent unchanged with reset=false. A zero/
// unknown resetAt never triggers a reset (behavior unchanged).
func effectiveUsedPct(usedPct int, resetAt int64, now time.Time) (pct int, reset bool) {
	if resetAt > 0 && now.Unix() >= resetAt {
		return 0, true
	}
	return usedPct, false
}

// formatPct renders a window's used-percent, prefixing a tilde when the
// value is inferred from a rolled-over window (e.g. "~0%") rather than
// observed.
func formatPct(pct int, inferred bool) string {
	if inferred {
		return fmt.Sprintf("~%d%%", pct)
	}
	return fmt.Sprintf("%d%%", pct)
}

// primaryResetDuration returns the time until the primary window
// resets, as of now.
//
//   - When the window has NOT reset (reset=false), it prefers
//     reset-after-seconds and falls back to reset-at minus now — the
//     original behavior.
//   - When the window HAS reset (reset=true), the stored reset-after /
//     reset-at are in the past; instead it counts down to the NEXT
//     reset, approximated as ResetAt + window. If the window length is
//     unknown (or the result is still not in the future) it returns 0
//     so no stale/negative annotation is shown.
func (rl CodexRateLimits) primaryResetDuration(now time.Time, reset bool) time.Duration {
	if reset {
		if rl.PrimaryResetAt > 0 && rl.PrimaryWindowMin > 0 {
			next := time.Unix(rl.PrimaryResetAt+int64(rl.PrimaryWindowMin)*60, 0)
			if d := next.Sub(now); d > 0 {
				return d
			}
		}
		return 0
	}
	if rl.PrimaryResetAfter > 0 {
		return time.Duration(rl.PrimaryResetAfter) * time.Second
	}
	if rl.PrimaryResetAt > 0 {
		if d := time.Unix(rl.PrimaryResetAt, 0).Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// windowLabel turns a window length in minutes into a short label
// ("5h", "7d"). Falls back to the provided default when the minutes
// are unknown or don't match a tidy hour/day count.
func windowLabel(minutes int, fallback string) string {
	switch {
	case minutes <= 0:
		return fallback
	case minutes%(60*24) == 0:
		return fmt.Sprintf("%dd", minutes/(60*24))
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// shortDuration renders a duration compactly: "4h32m", "12m", "45s".
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
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
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second // idle/inactivity timeout (was a 120s whole-request cap)
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
		transport.ResponseHeaderTimeout = 0                 // do NOT cap header wait: a slow model may delay first byte
		cfg.HTTPClient = &http.Client{Transport: transport} // no Client.Timeout: streaming body must not be capped
	}
	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	p := &CodexProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps}
	// Seed the snapshot from disk so the HUD `limit:` tile renders the
	// last known usage immediately, before any /responses call. This
	// reads a local file only — it never performs a network request.
	// Load this account's last snapshot so the HUD tile shows its
	// own numbers immediately. cfg.AccountID scopes the file per
	// account (empty = legacy shared file). The first real response
	// re-saves under the live account id from the token.
	if rl, ok := loadCodexRateLimits(cfg.DataDir, cfg.AccountID); ok {
		p.rl = rl
	}
	return p, nil
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
		// Derive a cancellable child context so the idle watchdog can
		// abort a stalled stream. cancel runs after the read completes.
		reqCtx, cancel := context.WithCancel(ctx)
		// notify surfaces non-terminal rate-limit/5xx retry notices to
		// the UI (same Delta.Notice channel openai.go/anthropic.go use);
		// it respects ctx so a cancelled run doesn't block on the send.
		notify := func(d Delta) {
			select {
			case out <- d:
			case <-ctx.Done():
			}
		}
		resp, err := p.doWithAuth(reqCtx, reqBody, notify)
		if err != nil {
			cancel()
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer cancel()
		body := newIdleTimeoutReader(resp.Body, p.cfg.Timeout, cancel)
		defer body.Close()
		p.streamCodexSSE(ctx, body, out)
	}()
	return out, nil
}

// doWithAuth performs the POST with bearer + ChatGPT headers.
// Three orthogonal retry paths interleave in one loop:
//   - 401: transparent token refresh (once);
//   - 400 effort-learn: patch reasoning effort from the error (once);
//   - 429/5xx: honour Retry-After (capped by rateLimitWaitBudget),
//     mirroring openai.go/anthropic.go. Each rate-limit retry emits a
//     non-terminal Delta.Notice via notify (may be nil), and after the
//     retries are exhausted the terminal error carries the same
//     "switch model" hint as the other providers.
func (p *CodexProvider) doWithAuth(ctx context.Context, body []byte, notify func(Delta)) (*http.Response, error) {
	access, accountID, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	effortRetried := false
	refreshed := false
	const maxRateLimitAttempts = 3
	rlAttempt := 0
	waitBudget := rateLimitWaitBudget
	for {
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
			// The ChatGPT backend stamps usage limits on the headers
			// of every 200 — refresh the HUD snapshot here (no extra
			// request). A non-Codex/empty header set yields OK=false
			// and is ignored by setRateLimits.
			p.setRateLimits(accountID, parseCodexRateLimits(resp.Header))
			return resp, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			// Transparent refresh + single retry.
			refreshed = true
			access, err = p.cfg.Tokens.Refresh(ctx)
			if err != nil {
				return nil, fmt.Errorf("codex auth expired and refresh failed: %w", err)
			}
			continue
		}
		if effort, ok := LearnReasoningEffortFromError(p.cfg.Model, resp.StatusCode, respBody); ok && !effortRetried {
			if patched, patchedOK := patchCodexReasoningEffort(body, effort); patchedOK {
				body = patched
				effortRetried = true
				continue
			}
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5) && rlAttempt < maxRateLimitAttempts-1 {
			rlAttempt++
			wait := retryWait(resp.Header, rlAttempt, waitBudget)
			waitBudget -= wait
			if notify != nil {
				notify(Delta{Notice: rateLimitNotice(p.cfg.Model, resp.StatusCode, wait, rlAttempt, maxRateLimitAttempts)})
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, fmt.Errorf("http %d: %s%s%s", resp.StatusCode, string(respBody), badRequestEffortHint(resp.StatusCode, respBody), rateLimitExhaustedHint(p.cfg.Model, resp.StatusCode))
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
	if e := ReasoningEffortForModel(model); e != "" && e != "none" {
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

func patchCodexReasoningEffort(body []byte, effort string) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if effort == "" || effort == "none" {
		delete(req, "reasoning")
	} else {
		reasoning, _ := req["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = make(map[string]any)
		}
		reasoning["effort"] = effort
		if _, ok := reasoning["summary"]; !ok {
			reasoning["summary"] = "auto"
		}
		req["reasoning"] = reasoning
	}
	out, err := json.Marshal(req)
	return out, err == nil
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
