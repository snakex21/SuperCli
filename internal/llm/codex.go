package llm

import (
	"context"
	"fmt"
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
	// Timeout bounds response-header wait and the maximum idle gap between
	// SSE bytes. It is not a whole-request cap; Codex streams can be long-lived.
	// Defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. Defaults to 30s.
	ConnectTimeout time.Duration
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
	// Capabilities, if nil, defaults to a registry lookup.
	Capabilities *CapabilityRegistry
	// StandardResponsesAPI switches the transport from the ChatGPT-specific
	// Codex backend dialect to the public OpenAI Responses API dialect. The
	// request/event schema is shared, but standard gateways must not receive
	// ChatGPT-only headers and static API keys cannot be refreshed through the
	// OAuth token source.
	StandardResponsesAPI bool
	// PromptCacheKey groups related standard Responses API calls for provider-
	// side prompt caching. It is ignored by the ChatGPT backend dialect.
	PromptCacheKey string
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
		transport.ResponseHeaderTimeout = 0                 // bounded per request below so custom clients behave the same
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
