package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"supercli/internal/system/uilang"
)

// usageEndpointURL derives the dedicated rate-limit ("usage") endpoint
// from the ChatGPT backend root, mirroring the Codex CLI reference
// (codex-rs backend-client/src/client.rs get_rate_limits_many +
// PathStyle::from_base_url).
//
// IMPORTANT: codex-rs roots the backend-client at the bare
// "/backend-api" base (see client.rs base_url normalization:
// `base_url = format!("{base_url}/backend-api")`) and forms the WHAM
// usage path as "{base}/wham/usage" → "https://chatgpt.com/backend-api/wham/usage".
// The "/codex" segment only belongs to the completions path
// ("/backend-api/codex/responses"), NOT to /wham/usage. Our
// CodexConfig.BackendURL bakes "/codex" into the root so that
// `BackendURL + "/responses"` is correct, so here we must strip a
// trailing "/codex" before building the WHAM path, otherwise the edge
// (chatgpt.com WAF) serves a 403 HTML page for the bogus
// "/backend-api/codex/wham/usage" path.
//
//   - When the base contains "/backend-api", the ChatGPT WHAM path
//     applies → "<backend-api root>/wham/usage" (with any trailing
//     "/codex" stripped).
//   - Otherwise the standalone Codex API path applies →
//     "<base>/api/codex/usage".
//
// The base is trimmed of any trailing slash first.
func usageEndpointURL(backendURL string) string {
	base := strings.TrimRight(backendURL, "/")
	if strings.Contains(base, "/backend-api") {
		base = strings.TrimSuffix(base, "/codex")
		return base + "/wham/usage"
	}
	return base + "/api/codex/usage"
}

// parseCodexUsageBody retains legacy scalar fields alongside the exact server snapshot.
func parseCodexUsageBody(body []byte) CodexRateLimits {
	now := time.Now()
	snapshot, ok := parseCodexUsageSnapshot(body, now)
	if !ok {
		return CodexRateLimits{}
	}
	rl := codexLimitsFromSnapshot(snapshot)
	var p codexUsagePayload
	_ = json.Unmarshal(body, &p)
	if p.RateLimit != nil {
		if w := p.RateLimit.PrimaryWindow; w != nil && w.ResetAfterSeconds != nil {
			rl.PrimaryResetAfter = *w.ResetAfterSeconds
		}
		if w := p.RateLimit.SecondaryWindow; w != nil && w.ResetAfterSeconds != nil {
			rl.SecondaryResetAfter = *w.ResetAfterSeconds
		}
	}
	return rl
}

// FormatDetail renders a multi-line, human-readable summary of the
// snapshot for the /usage slash command — one line per window with the
// window label, used-percent, and time until reset. Reset-aware in the
// same way as FormatHUD (a rolled-over window shows ~0%). Returns a
// short placeholder when the snapshot is empty.
func (rl CodexRateLimits) FormatDetail() string {
	return rl.FormatDetailFor(uilang.English)
}

// FormatDetailFor localizes presentation without changing quota data or protocol fields.
func (rl CodexRateLimits) FormatDetailFor(language string) string {
	return rl.formatDetailAtFor(time.Now(), language)
}

func (rl CodexRateLimits) formatDetailAt(now time.Time) string {
	return rl.formatDetailAtFor(now, uilang.English)
}

func (rl CodexRateLimits) formatDetailAtFor(now time.Time, language string) string {
	if !rl.OK {
		return uilang.Text(language, "app.usage_detail.none")
	}
	if rl.Snapshot != nil {
		return formatCodexUsageDetail(*rl.Snapshot, now, language)
	}
	var b strings.Builder
	pPct, pReset := effectiveUsedPct(rl.PrimaryUsedPct, rl.PrimaryResetAt, now)
	fmt.Fprintf(&b, uilang.Text(language, "app.usage_detail.window"),
		windowLabel(rl.PrimaryWindowMin, "primary"), formatPct(pPct, pReset))
	if d := rl.primaryResetDuration(now, pReset); d > 0 {
		fmt.Fprintf(&b, uilang.Text(language, "app.usage_detail.reset"), shortDuration(d))
	}
	sPct, sReset := effectiveUsedPct(rl.SecondaryUsedPct, rl.SecondaryResetAt, now)
	fmt.Fprintf(&b, "\n"+uilang.Text(language, "app.usage_detail.window"),
		windowLabel(rl.SecondaryWindowMin, "secondary"), formatPct(sPct, sReset))
	if d := windowResetDuration(rl.SecondaryResetAt, rl.SecondaryWindowMin, now, sReset); d > 0 {
		fmt.Fprintf(&b, uilang.Text(language, "app.usage_detail.reset"), shortDuration(d))
	}
	return b.String()
}

func formatCodexUsageDetail(snapshot CodexUsageSnapshot, now time.Time, language string) string {
	snapshot = snapshot.At(now)
	var lines []string
	if snapshot.PlanType != "" {
		lines = append(lines, uilang.Text(language, "acct.usage.plan")+": "+snapshot.PlanType)
	}
	unknown := uilang.Text(language, "acct.usage.unknown")
	for _, limit := range snapshot.RateLimits {
		if limit.ID != "codex" {
			name := limit.Name
			if name == "" {
				name = limit.ID
			}
			lines = append(lines, name+":")
		}
		windows := []*CodexUsageWindow{limit.Primary, limit.Secondary}
		if limit.Primary == nil && limit.Secondary == nil {
			lines = append(lines, uilang.Text(language, "acct.usage.windowUnknown")+": "+unknown)
		}
		for _, w := range windows {
			if w == nil {
				continue
			}
			label := codexWindowName(w, uilang.Text(language, "acct.usage.windowUnknown"))
			pct := unknown
			if w.UsedPercent != nil {
				pct = fmt.Sprintf("%g%%", *w.UsedPercent)
			}
			line := fmt.Sprintf(uilang.Text(language, "app.usage_detail.window"), label, pct)
			if w.RemainingPercent != nil {
				line += fmt.Sprintf(" · %s %g%%", uilang.Text(language, "acct.usage.remaining"), *w.RemainingPercent)
			}
			if w.Stale {
				line += " · " + uilang.Text(language, "acct.usage.stale")
			} else if w.ResetsAt != nil && *w.ResetsAt > now.Unix() {
				line += fmt.Sprintf(uilang.Text(language, "app.usage_detail.reset"), shortDuration(time.Duration(*w.ResetsAt-now.Unix())*time.Second))
			}
			lines = append(lines, line)
		}
	}
	if snapshot.Credits != nil {
		c := snapshot.Credits
		value := unknown
		if c.Unlimited != nil && *c.Unlimited {
			value = uilang.Text(language, "acct.usage.unlimited")
		} else if c.Balance != nil {
			value = *c.Balance
		} else if c.HasCredits != nil && !*c.HasCredits {
			value = uilang.Text(language, "acct.usage.noCredits")
		}
		lines = append(lines, uilang.Text(language, "acct.usage.credits")+": "+value)
	}
	if len(lines) == 0 {
		return uilang.Text(language, "acct.usage.noSnapshot")
	}
	return strings.Join(lines, "\n")
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

// snippet returns a single-line, length-capped rendering of a response
// body for use in error messages — newlines collapsed to spaces so the
// /usage output stays readable, truncated with an ellipsis past max.
func snippet(body []byte, max int) string {
	s := strings.TrimSpace(string(body))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "(empty body)"
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// FetchUsage performs a lightweight GET against the dedicated usage
// endpoint to refresh the rate-limit snapshot WITHOUT a completion —
// it does not consume the quota the way /responses does. On success it
// stores the snapshot via setRateLimits (which also persists it to
// disk), so the next HUD render and the next process start both see
// fresh numbers. The token is obtained through the configured token
// source and refreshed once on a 401, mirroring doWithAuth.
//
// Refresh is on demand; reading cached usage never makes a network call.
func (p *CodexProvider) FetchUsage(ctx context.Context) (CodexRateLimits, error) {
	if p.cfg.Tokens == nil {
		return CodexRateLimits{}, fmt.Errorf("codex usage: no token source")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	access, accountID, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return CodexRateLimits{}, fmt.Errorf("codex usage: could not obtain access token")
	}
	if p.cfg.AccountID != "" && accountID != p.cfg.AccountID {
		return CodexRateLimits{}, fmt.Errorf("codex usage: account identity changed; rebuild provider")
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
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024+1))
			resp.Body.Close()
			if readErr != nil {
				return CodexRateLimits{}, fmt.Errorf("codex usage: read response: %w", readErr)
			}
			if len(body) > 256*1024 {
				return CodexRateLimits{}, fmt.Errorf("codex usage: response exceeds 256 KiB")
			}
			rl := parseCodexUsageBody(body)
			if !rl.OK {
				return rl, fmt.Errorf("codex usage: unexpected JSON shape")
			}
			p.setRateLimits(accountID, rl)
			return rl, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 1 {
			access, err = p.cfg.Tokens.Refresh(ctx)
			if err != nil {
				return CodexRateLimits{}, fmt.Errorf("codex auth expired and refresh failed")
			}
			// Refresh can change account identity; never attribute the retry to the old account.
			access, accountID, err = p.cfg.Tokens.Token(ctx)
			if err != nil {
				return CodexRateLimits{}, fmt.Errorf("codex usage: token unavailable after refresh")
			}
			if p.cfg.AccountID != "" && accountID != p.cfg.AccountID {
				return CodexRateLimits{}, fmt.Errorf("codex usage: account identity changed; rebuild provider")
			}
			continue
		}
		// Always include the URL: a 404 here almost always means the
		// usage path is wrong for this backend root, and the user needs
		// to see which URL was hit to diagnose it.
		return CodexRateLimits{}, fmt.Errorf("codex usage: http %d", resp.StatusCode)
	}
}
