package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CodexUsageSnapshot contains observed account limits. Nil values are unknown,
// not zero usage or an unlimited entitlement. Windows retain server durations.
type CodexUsageSnapshot struct {
	CapturedAt   *time.Time         `json:"captured_at"`
	Source       string             `json:"source"`
	Stale        bool               `json:"stale"`
	Availability string             `json:"availability"`
	PlanType     string             `json:"plan_type,omitempty"`
	RateLimits   []CodexUsageLimit  `json:"rate_limits"`
	Credits      *CodexUsageCredits `json:"credits"`
}

type CodexUsageLimit struct {
	CapturedAt   *time.Time        `json:"captured_at,omitempty"`
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Allowed      *bool             `json:"allowed"`
	LimitReached *bool             `json:"limit_reached"`
	Primary      *CodexUsageWindow `json:"primary"`
	Secondary    *CodexUsageWindow `json:"secondary"`
}

type CodexUsageWindow struct {
	UsedPercent      *float64 `json:"used_percent"`
	RemainingPercent *float64 `json:"remaining_percent"`
	WindowSeconds    *int64   `json:"window_seconds"`
	ResetsAt         *int64   `json:"resets_at"`
	Stale            bool     `json:"stale"`
}

type CodexUsageCredits struct {
	CapturedAt *time.Time `json:"captured_at,omitempty"`
	HasCredits *bool      `json:"has_credits"`
	Unlimited  *bool      `json:"unlimited"`
	Balance    *string    `json:"balance"`
}

type CodexUsageSummary struct {
	Accounts  int `json:"accounts"`
	Known     int `json:"known"`
	Unknown   int `json:"unknown"`
	Stale     int `json:"stale"`
	Exhausted int `json:"exhausted"`
	Available int `json:"available"`
}

const codexUsageFreshFor = 15 * time.Minute

func codexCapturedStale(at *time.Time, now time.Time) bool {
	return at == nil || at.IsZero() || now.Sub(*at) >= codexUsageFreshFor || at.After(now.Add(time.Minute))
}

func codexPtr[T any](v T) *T { return &v }

func cloneCodexPtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	return codexPtr(*v)
}

func cloneCodexUsageWindow(w *CodexUsageWindow) *CodexUsageWindow {
	if w == nil {
		return nil
	}
	out := *w
	out.UsedPercent = cloneCodexPtr(w.UsedPercent)
	out.RemainingPercent = cloneCodexPtr(w.RemainingPercent)
	out.WindowSeconds = cloneCodexPtr(w.WindowSeconds)
	out.ResetsAt = cloneCodexPtr(w.ResetsAt)
	return &out
}

// At returns an owned snapshot with freshness recomputed, without assuming that
// an expired window reset to zero while the application was offline.
func (s CodexUsageSnapshot) At(now time.Time) CodexUsageSnapshot {
	s.CapturedAt = cloneCodexPtr(s.CapturedAt)
	ageStale := codexCapturedStale(s.CapturedAt, now)
	s.Stale = ageStale
	out := make([]CodexUsageLimit, len(s.RateLimits))
	for i, limit := range s.RateLimits {
		limit.CapturedAt = cloneCodexPtr(limit.CapturedAt)
		limitStale := ageStale
		if limit.CapturedAt != nil {
			limitStale = codexCapturedStale(limit.CapturedAt, now)
		}
		limit.Allowed = cloneCodexPtr(limit.Allowed)
		limit.LimitReached = cloneCodexPtr(limit.LimitReached)
		limit.Primary, limit.Secondary = cloneCodexUsageWindow(limit.Primary), cloneCodexUsageWindow(limit.Secondary)
		for _, w := range []*CodexUsageWindow{limit.Primary, limit.Secondary} {
			if w != nil {
				w.Stale = limitStale || (w.ResetsAt != nil && *w.ResetsAt > 0 && now.Unix() >= *w.ResetsAt)
				if w.Stale {
					s.Stale = true
				}
			}
		}
		out[i] = limit
	}
	s.RateLimits = out
	if s.Credits != nil {
		c := *s.Credits
		c.CapturedAt = cloneCodexPtr(c.CapturedAt)
		if c.CapturedAt != nil && codexCapturedStale(c.CapturedAt, now) {
			s.Stale = true
		}
		c.HasCredits, c.Unlimited, c.Balance = cloneCodexPtr(c.HasCredits), cloneCodexPtr(c.Unlimited), cloneCodexPtr(c.Balance)
		s.Credits = &c
	}
	known, exhausted := s.availability(now)
	s.Availability = "unknown"
	if known {
		s.Availability = "available"
		if exhausted {
			s.Availability = "exhausted"
		}
	}
	return s
}

// availability only considers the general Codex bucket: a model-specific
// additional bucket cannot establish whether this account can serve all models.
func (s CodexUsageSnapshot) availability(now time.Time) (known, exhausted bool) {
	if codexCapturedStale(s.CapturedAt, now) {
		return false, false
	}
	for _, limit := range s.RateLimits {
		if limit.ID != "codex" {
			continue
		}
		if limit.CapturedAt != nil && codexCapturedStale(limit.CapturedAt, now) {
			return false, false
		}
		for _, w := range []*CodexUsageWindow{limit.Primary, limit.Secondary} {
			if w != nil && w.ResetsAt != nil && *w.ResetsAt > 0 && now.Unix() >= *w.ResetsAt {
				return false, false
			}
		}
		if limit.Allowed != nil {
			return true, !*limit.Allowed
		}
		if limit.LimitReached != nil && *limit.LimitReached {
			return true, true
		}
		for _, w := range []*CodexUsageWindow{limit.Primary, limit.Secondary} {
			if w != nil && w.UsedPercent != nil {
				known = true
				if *w.UsedPercent >= 100 {
					exhausted = true
				}
			}
		}
		return known, exhausted
	}
	return false, false
}

func (s CodexUsageSnapshot) AvailabilityAt(now time.Time) (known, exhausted bool) {
	return s.availability(now)
}

// ExhaustedAt is conservative: unknown or expired observations do not exclude
// an account. An explicit server allowed=true remains authoritative (credits).
func (rl CodexRateLimits) ExhaustedAt(now time.Time) bool {
	if !rl.OK || rl.Snapshot == nil {
		return false
	}
	_, exhausted := rl.Snapshot.availability(now)
	return exhausted
}

func (s *CodexUsageSummary) add(snapshot *CodexUsageSnapshot, now time.Time) {
	s.Accounts++
	if snapshot == nil {
		s.Unknown++
		return
	}
	if snapshot.At(now).Stale {
		s.Stale++
	}
	known, exhausted := snapshot.availability(now)
	if !known {
		s.Unknown++
		return
	}
	s.Known++
	if exhausted {
		s.Exhausted++
	} else {
		s.Available++
	}
}

type codexUsagePayload struct {
	PlanType             string                     `json:"plan_type"`
	RateLimit            *codexUsageRateLimit       `json:"rate_limit"`
	AdditionalRateLimits []codexAdditionalRateLimit `json:"additional_rate_limits"`
	Credits              *CodexUsageCredits         `json:"credits"`
}

type codexAdditionalRateLimit struct {
	LimitName      string               `json:"limit_name"`
	MeteredFeature string               `json:"metered_feature"`
	RateLimit      *codexUsageRateLimit `json:"rate_limit"`
}

type codexUsageRateLimit struct {
	Allowed         *bool             `json:"allowed"`
	LimitReached    *bool             `json:"limit_reached"`
	PrimaryWindow   *codexUsageWindow `json:"primary_window"`
	SecondaryWindow *codexUsageWindow `json:"secondary_window"`
}

type codexUsageWindow struct {
	UsedPercent       *float64 `json:"used_percent"`
	LimitWindowSecs   *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds *int64   `json:"reset_after_seconds"`
	ResetAt           *int64   `json:"reset_at"`
}

func observedCodexWindow(w *codexUsageWindow, now time.Time) *CodexUsageWindow {
	if w == nil {
		return nil
	}
	out := &CodexUsageWindow{}
	if w.UsedPercent != nil && *w.UsedPercent >= 0 && !math.IsNaN(*w.UsedPercent) && !math.IsInf(*w.UsedPercent, 0) {
		pct := *w.UsedPercent
		out.UsedPercent, out.RemainingPercent = codexPtr(pct), codexPtr(math.Max(0, 100-pct))
	}
	if w.LimitWindowSecs != nil && *w.LimitWindowSecs > 0 {
		out.WindowSeconds = cloneCodexPtr(w.LimitWindowSecs)
	}
	if w.ResetAt != nil && *w.ResetAt > 0 {
		out.ResetsAt = cloneCodexPtr(w.ResetAt)
	} else if w.ResetAfterSeconds != nil && *w.ResetAfterSeconds > 0 && *w.ResetAfterSeconds <= math.MaxInt64-now.Unix() {
		out.ResetsAt = codexPtr(now.Unix() + *w.ResetAfterSeconds)
	}
	return out
}

func observedCodexLimit(id, name string, r *codexUsageRateLimit, now time.Time) CodexUsageLimit {
	return CodexUsageLimit{CapturedAt: codexPtr(now), ID: id, Name: name, Allowed: r.Allowed, LimitReached: r.LimitReached,
		Primary: observedCodexWindow(r.PrimaryWindow, now), Secondary: observedCodexWindow(r.SecondaryWindow, now)}
}

func parseCodexUsageSnapshot(body []byte, now time.Time) (CodexUsageSnapshot, bool) {
	var p codexUsagePayload
	if json.Unmarshal(body, &p) != nil {
		return CodexUsageSnapshot{}, false
	}
	s := CodexUsageSnapshot{CapturedAt: codexPtr(now), Source: "usage", PlanType: p.PlanType, Credits: p.Credits, RateLimits: []CodexUsageLimit{}}
	if s.Credits != nil {
		s.Credits.CapturedAt = codexPtr(now)
	}
	usable := p.Credits != nil
	if p.RateLimit != nil {
		r := p.RateLimit
		usable = usable || r.Allowed != nil || r.LimitReached != nil || r.PrimaryWindow != nil || r.SecondaryWindow != nil
		s.RateLimits = append(s.RateLimits, observedCodexLimit("codex", "", r, now))
	}
	for i, a := range p.AdditionalRateLimits {
		if a.RateLimit == nil {
			continue
		}
		id := a.MeteredFeature
		if id == "" {
			id = a.LimitName
		}
		if id == "" || id == "codex" {
			id = fmt.Sprintf("additional-%d", i+1)
		}
		s.RateLimits = append(s.RateLimits, observedCodexLimit(id, a.LimitName, a.RateLimit, now))
		usable = true
	}
	return s.At(now), usable
}

func codexLimitsFromSnapshot(s CodexUsageSnapshot) CodexRateLimits {
	rl := CodexRateLimits{OK: true, Snapshot: &s}
	for _, r := range s.RateLimits {
		if r.ID != "codex" {
			continue
		}
		if w := r.Primary; w != nil {
			if w.UsedPercent != nil {
				rl.PrimaryUsedPct = int(math.Min(100, *w.UsedPercent))
			}
			if w.WindowSeconds != nil {
				rl.PrimaryWindowMin = int(*w.WindowSeconds / 60)
			}
			if w.ResetsAt != nil {
				rl.PrimaryResetAt = *w.ResetsAt
			}
		}
		if w := r.Secondary; w != nil {
			if w.UsedPercent != nil {
				rl.SecondaryUsedPct = int(math.Min(100, *w.UsedPercent))
			}
			if w.WindowSeconds != nil {
				rl.SecondaryWindowMin = int(*w.WindowSeconds / 60)
			}
			if w.ResetsAt != nil {
				rl.SecondaryResetAt = *w.ResetsAt
			}
		}
		break
	}
	return rl
}

func codexSnapshotFromHeaders(h http.Header, now time.Time) *CodexUsageSnapshot {
	s := CodexUsageSnapshot{CapturedAt: codexPtr(now), Source: "headers", RateLimits: []CodexUsageLimit{{ID: "codex", CapturedAt: codexPtr(now)}}}
	for i, name := range []string{"Primary", "Secondary"} {
		prefix := "X-Codex-" + name + "-"
		f, err := strconv.ParseFloat(strings.TrimSpace(h.Get(prefix+"Used-Percent")), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			continue
		}
		seconds := int64(parseIntHeader(h.Get(prefix+"Window-Minutes"))) * 60
		at, after := parseInt64Header(h.Get(prefix+"Reset-At")), parseInt64Header(h.Get(prefix+"Reset-After-Seconds"))
		w := observedCodexWindow(&codexUsageWindow{UsedPercent: &f, LimitWindowSecs: &seconds, ResetAt: &at, ResetAfterSeconds: &after}, now)
		if i == 0 {
			s.RateLimits[0].Primary = w
		} else {
			s.RateLimits[0].Secondary = w
		}
	}
	return &s
}

func codexWindowName(w *CodexUsageWindow, fallback string) string {
	if w == nil || w.WindowSeconds == nil {
		return fallback
	}
	seconds := *w.WindowSeconds
	if seconds%86400 == 0 {
		return fmt.Sprintf("%dd", seconds/86400)
	}
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func (s CodexUsageSnapshot) formatHUD(now time.Time) string {
	s = s.At(now)
	var parts []string
	for _, limit := range s.RateLimits {
		if limit.ID != "codex" {
			continue
		}
		for i, w := range []*CodexUsageWindow{limit.Primary, limit.Secondary} {
			if w == nil {
				continue
			}
			name := []string{"primary", "secondary"}[i]
			pct := "?"
			if w.UsedPercent != nil {
				pct = fmt.Sprintf("%g%%", *w.UsedPercent)
			}
			part := codexWindowName(w, name) + " " + pct
			if w.Stale {
				part += " ?"
			} else if w.ResetsAt != nil && *w.ResetsAt > now.Unix() {
				part += " (" + shortDuration(time.Duration(*w.ResetsAt-now.Unix())*time.Second) + ")"
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "Codex ?"
	}
	return strings.Join(parts, " · ")
}

func legacyCodexUsageSnapshot(rl CodexRateLimits, captured time.Time) CodexUsageSnapshot {
	s := CodexUsageSnapshot{Source: "cache", RateLimits: []CodexUsageLimit{{ID: "codex"}}}
	if !captured.IsZero() {
		s.CapturedAt = codexPtr(captured)
	}
	for i := 0; i < 2; i++ {
		pct, minutes, at := rl.PrimaryUsedPct, rl.PrimaryWindowMin, rl.PrimaryResetAt
		if i == 1 {
			pct, minutes, at = rl.SecondaryUsedPct, rl.SecondaryWindowMin, rl.SecondaryResetAt
		}
		// Legacy scalar files cannot distinguish a missing zero from observed
		// zero. Only windows with corroborating fields can safely be surfaced.
		if pct == 0 && minutes == 0 && at == 0 {
			continue
		}
		w := &CodexUsageWindow{UsedPercent: codexPtr(float64(clampPct(pct))), RemainingPercent: codexPtr(float64(100 - clampPct(pct)))}
		if minutes > 0 {
			w.WindowSeconds = codexPtr(int64(minutes) * 60)
		}
		if at > 0 {
			w.ResetsAt = codexPtr(at)
		}
		if i == 0 {
			s.RateLimits[0].Primary = w
		} else {
			s.RateLimits[0].Secondary = w
		}
	}
	return s
}

func (p *CodexProvider) UsageSnapshot() (CodexUsageSnapshot, bool) {
	rl, ok := p.RateLimits()
	if !ok || rl.Snapshot == nil {
		return CodexUsageSnapshot{}, false
	}
	return rl.Snapshot.At(time.Now()), true
}
