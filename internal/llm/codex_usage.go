package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// usageEndpointURL derives the dedicated rate-limit ("usage") endpoint
// from the ChatGPT backend root, mirroring the Codex CLI reference
// (codex-rs backend-client/src/client.rs get_rate_limits_many +
// PathStyle::from_base_url):
//
//   - When the base contains "/backend-api" (our DefaultBackendURL,
//     "https://chatgpt.com/backend-api/codex"), the ChatGPT WHAM paths
//     apply → "<base>/wham/usage".
//   - Otherwise the standalone Codex API paths apply →
//     "<base>/api/codex/usage".
//
// The base is trimmed of any trailing slash first.
func usageEndpointURL(backendURL string) string {
	base := strings.TrimRight(backendURL, "/")
	if strings.Contains(base, "/backend-api") {
		return base + "/wham/usage"
	}
	return base + "/api/codex/usage"
}

// codexUsagePayload is the (subset of the) JSON body returned by the
// usage endpoint. Field names match the Codex backend OpenAPI models
// (RateLimitStatusPayload / RateLimitStatusDetails /
// RateLimitWindowSnapshot). Only the fields we surface in the HUD are
// modeled; everything else is ignored. Unlike /responses (which stamps
// X-Codex-* response HEADERS), this endpoint returns the snapshot in
// the BODY, so it needs its own parser.
type codexUsagePayload struct {
	RateLimit *codexUsageRateLimit `json:"rate_limit"`
}

type codexUsageRateLimit struct {
	PrimaryWindow   *codexUsageWindow `json:"primary_window"`
	SecondaryWindow *codexUsageWindow `json:"secondary_window"`
}

type codexUsageWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	LimitWindowSecs   int64   `json:"limit_window_seconds"`
	ResetAfterSeconds int64   `json:"reset_after_seconds"`
	ResetAt           int64   `json:"reset_at"`
}

// parseCodexUsageBody maps the usage-endpoint JSON body onto a
// CodexRateLimits snapshot. It is the body-shaped counterpart to
// parseCodexRateLimits (which reads X-Codex-* headers). OK is set when
// at least one window carried a usable used-percent. Empty/garbage
// input yields OK=false rather than an error, so a malformed response
// degrades to "keep the old snapshot" instead of breaking the caller.
//
// window_minutes is derived from limit_window_seconds (seconds/60),
// matching the Codex CLI's window_minutes_from_seconds.
func parseCodexUsageBody(body []byte) CodexRateLimits {
	var rl CodexRateLimits
	var p codexUsagePayload
	if err := json.Unmarshal(body, &p); err != nil || p.RateLimit == nil {
		return rl
	}
	if w := p.RateLimit.PrimaryWindow; w != nil {
		rl.PrimaryUsedPct = clampPct(int(w.UsedPercent))
		rl.PrimaryWindowMin = int(w.LimitWindowSecs / 60)
		rl.PrimaryResetAt = w.ResetAt
		rl.PrimaryResetAfter = w.ResetAfterSeconds
		rl.OK = true
	}
	if w := p.RateLimit.SecondaryWindow; w != nil {
		rl.SecondaryUsedPct = clampPct(int(w.UsedPercent))
		rl.SecondaryWindowMin = int(w.LimitWindowSecs / 60)
		rl.SecondaryResetAt = w.ResetAt
		rl.SecondaryResetAfter = w.ResetAfterSeconds
		rl.OK = true
	}
	return rl
}

// FormatDetail renders a multi-line, human-readable summary of the
// snapshot for the /usage slash command — one line per window with the
// window label, used-percent, and time until reset. Reset-aware in the
// same way as FormatHUD (a rolled-over window shows ~0%). Returns a
// short placeholder when the snapshot is empty.
func (rl CodexRateLimits) FormatDetail() string {
	return rl.formatDetailAt(time.Now())
}

func (rl CodexRateLimits) formatDetailAt(now time.Time) string {
	if !rl.OK {
		return "no Codex usage data yet."
	}
	var b strings.Builder
	pPct, pReset := effectiveUsedPct(rl.PrimaryUsedPct, rl.PrimaryResetAt, now)
	fmt.Fprintf(&b, "%s window: %s used",
		windowLabel(rl.PrimaryWindowMin, "5h"), formatPct(pPct, pReset))
	if d := rl.primaryResetDuration(now, pReset); d > 0 {
		fmt.Fprintf(&b, " · resets in %s", shortDuration(d))
	}
	sPct, sReset := effectiveUsedPct(rl.SecondaryUsedPct, rl.SecondaryResetAt, now)
	fmt.Fprintf(&b, "\n%s window: %s used",
		windowLabel(rl.SecondaryWindowMin, "7d"), formatPct(sPct, sReset))
	if d := windowResetDuration(rl.SecondaryResetAt, rl.SecondaryWindowMin, now, sReset); d > 0 {
		fmt.Fprintf(&b, " · resets in %s", shortDuration(d))
	}
	return b.String()
}

// windowResetDuration is the generic reset-countdown used for the
// secondary window in FormatDetail. It mirrors primaryResetDuration but
// takes the window's fields explicitly (the secondary window has no
// dedicated reset-after field today, so it relies on resetAt).
func windowResetDuration(resetAt int64, windowMin int, now time.Time, reset bool) time.Duration {
	if reset {
		if resetAt > 0 && windowMin > 0 {
			next := time.Unix(resetAt+int64(windowMin)*60, 0)
			if d := next.Sub(now); d > 0 {
				return d
			}
		}
		return 0
	}
	if resetAt > 0 {
		if d := time.Unix(resetAt, 0).Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// FetchUsage performs a lightweight GET against the dedicated usage
// endpoint to refresh the rate-limit snapshot WITHOUT a completion —
// it does not consume the quota the way /responses does. On success it
// stores the snapshot via setRateLimits (which also persists it to
// disk), so the next HUD render and the next process start both see
// fresh numbers. The token is obtained through the configured token
// source and refreshed once on a 401, mirroring doWithAuth.
//
// Callers may invoke this asynchronously (e.g. in a goroutine at
// startup / after a model swap); it never mutates shared state except
// through the existing rlMu-guarded setRateLimits.
func (p *CodexProvider) FetchUsage(ctx context.Context) (CodexRateLimits, error) {
	if p.cfg.Tokens == nil {
		return CodexRateLimits{}, fmt.Errorf("codex usage: no token source")
	}
	access, accountID, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return CodexRateLimits{}, err
	}
	url := usageEndpointURL(p.cfg.BackendURL)
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return CodexRateLimits{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("originator", "codex_cli_go")
		if accountID != "" {
			req.Header.Set("chatgpt-account-id", accountID)
		}
		resp, err := p.http.Do(req)
		if err != nil {
			return CodexRateLimits{}, fmt.Errorf("http: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
			resp.Body.Close()
			rl := parseCodexUsageBody(body)
			if !rl.OK {
				return rl, fmt.Errorf("codex usage: response carried no usable limits")
			}
			p.setRateLimits(accountID, rl)
			return rl, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 1 {
			access, err = p.cfg.Tokens.Refresh(ctx)
			if err != nil {
				return CodexRateLimits{}, fmt.Errorf("codex auth expired and refresh failed: %w", err)
			}
			continue
		}
		return CodexRateLimits{}, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}
