package claudeauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const UsageURL = "https://api.anthropic.com/api/oauth/usage"

type UsageWindow struct {
	ID               string     `json:"id"`
	WindowSeconds    int64      `json:"window_seconds"`
	UsedPercent      *float64   `json:"used_percent"`
	RemainingPercent *float64   `json:"remaining_percent"`
	ResetsAt         *time.Time `json:"resets_at"`
	CapturedAt       time.Time  `json:"captured_at"`
	Stale            bool       `json:"stale"`
}

type ExtraUsage struct {
	Enabled     bool     `json:"enabled"`
	UsedPercent *float64 `json:"used_percent"`
}

type UsageSnapshot struct {
	CapturedAt time.Time     `json:"captured_at"`
	Source     string        `json:"source"`
	Windows    []UsageWindow `json:"windows"`
	ExtraUsage *ExtraUsage   `json:"extra_usage,omitempty"`
	Stale      bool          `json:"stale"`
}

type usageFile struct {
	Identity string        `json:"identity"`
	Usage    UsageSnapshot `json:"usage"`
}

func tokenIdentity(t Tokens) string {
	value := t.LoginID
	if strings.HasPrefix(value, "sha256:") {
		return strings.TrimPrefix(value, "sha256:")
	}
	if value == "" {
		value = t.AccountID
	}
	if value == "" {
		value = t.RefreshToken
	}
	if value == "" {
		value = t.AccessToken
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func percent(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100 {
		return nil
	}
	n := *v
	return &n
}

func (s UsageSnapshot) At(now time.Time) UsageSnapshot {
	s.Windows = append([]UsageWindow(nil), s.Windows...)
	s.Stale = s.CapturedAt.IsZero() || now.Sub(s.CapturedAt) >= 15*time.Minute || s.CapturedAt.After(now.Add(time.Minute))
	for i := range s.Windows {
		w := &s.Windows[i]
		w.UsedPercent = percent(w.UsedPercent)
		w.RemainingPercent = nil
		w.Stale = w.CapturedAt.IsZero() || now.Sub(w.CapturedAt) >= 15*time.Minute || w.CapturedAt.After(now.Add(time.Minute)) || (w.ResetsAt != nil && !now.Before(*w.ResetsAt))
		if w.UsedPercent != nil {
			remaining := 100 - *w.UsedPercent
			w.RemainingPercent = &remaining
		}
	}
	return s
}

// Usage reads only this installation's saved account snapshot. A missing or
// expired window is never presented as a fresh, unused subscription.
func (m *Manager) Usage() *UsageSnapshot {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	t, err := m.load()
	if err != nil {
		return nil
	}
	var stored usageFile
	b, err := os.ReadFile(filepath.Join(filepath.Dir(m.path), "claude-usage.json"))
	if err != nil || json.Unmarshal(b, &stored) != nil || stored.Identity != tokenIdentity(t) {
		return nil
	}
	s := stored.Usage.At(time.Now())
	return &s
}

func (m *Manager) saveUsage(identity string, usage UsageSnapshot) error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	t, err := m.load()
	if err != nil {
		return err
	}
	if identity != tokenIdentity(t) {
		return fmt.Errorf("Claude account changed during usage refresh")
	}
	b, err := json.Marshal(usageFile{Identity: identity, Usage: usage})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".claude-usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(filepath.Dir(m.path), "claude-usage.json"))
}

func (m *Manager) RefreshUsage(ctx context.Context) (*UsageSnapshot, error) {
	m.state.usage.Lock()
	defer m.state.usage.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, err := m.AccessToken(ctx, false)
	if err != nil {
		return nil, err
	}
	m.state.mu.Lock()
	t, err := m.load()
	identity := tokenIdentity(t)
	m.state.mu.Unlock()
	if err != nil {
		return nil, err
	}
	endpoint := m.opts.UsageURL
	if endpoint == "" {
		endpoint = UsageURL
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", Beta)
		req.Header.Set("Accept", "application/json")
		resp, err := m.opts.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Claude subscription usage unavailable")
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			token, err = m.AccessToken(ctx, true)
			if err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("Claude subscription usage unavailable (HTTP %d)", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("Claude subscription usage response incomplete")
		}
		snapshot, err := parseUsage(b, time.Now())
		if err != nil {
			return nil, err
		}
		if err = m.saveUsage(identity, snapshot); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}
	return nil, fmt.Errorf("Claude subscription usage unavailable")
}

func parseUsage(b []byte, now time.Time) (UsageSnapshot, error) {
	var payload map[string]json.RawMessage
	if json.Unmarshal(b, &payload) != nil {
		return UsageSnapshot{}, fmt.Errorf("Claude returned invalid subscription usage")
	}
	s := UsageSnapshot{CapturedAt: now, Source: "account", Windows: []UsageWindow{}}
	for _, item := range []struct {
		id      string
		seconds int64
	}{{"five_hour", 18000}, {"seven_day", 604800}, {"seven_day_opus", 604800}, {"seven_day_sonnet", 604800}, {"seven_day_oauth_apps", 604800}} {
		raw := payload[item.id]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var window struct {
			Utilization *float64   `json:"utilization"`
			ResetsAt    *time.Time `json:"resets_at"`
		}
		if json.Unmarshal(raw, &window) != nil {
			continue
		}
		used := percent(window.Utilization)
		if used == nil && window.ResetsAt == nil {
			continue
		}
		s.Windows = append(s.Windows, UsageWindow{ID: item.id, WindowSeconds: item.seconds, UsedPercent: used, ResetsAt: window.ResetsAt, CapturedAt: now})
	}
	if raw := payload["extra_usage"]; len(raw) > 0 && string(raw) != "null" {
		var extra struct {
			Enabled     bool     `json:"is_enabled"`
			Utilization *float64 `json:"utilization"`
		}
		if json.Unmarshal(raw, &extra) == nil {
			s.ExtraUsage = &ExtraUsage{Enabled: extra.Enabled, UsedPercent: percent(extra.Utilization)}
		}
	}
	if len(s.Windows) == 0 && s.ExtraUsage == nil {
		return UsageSnapshot{}, fmt.Errorf("Claude has not reported subscription usage")
	}
	return s.At(now), nil
}

// ObserveUsageHeaders reuses quota information from actual model responses.
// It performs no network request and rejects responses from a previous login.
func (m *Manager) ObserveUsageHeaders(headers http.Header, accessToken string) {
	m.state.mu.Lock()
	t, err := m.load()
	identity := tokenIdentity(t)
	m.state.mu.Unlock()
	if err != nil || t.AccessToken != accessToken {
		return
	}
	current := m.Usage()
	now := time.Now()
	s := UsageSnapshot{Source: "response_headers", CapturedAt: now, Windows: []UsageWindow{}}
	if current != nil {
		s = *current
		s.CapturedAt = now
		s.Source = "response_headers"
	}
	changed := false
	for _, item := range []struct {
		id, short string
		seconds   int64
	}{{"five_hour", "5h", 18000}, {"seven_day", "7d", 604800}} {
		value := headers.Get("anthropic-ratelimit-unified-" + item.short + "-utilization")
		if value == "" {
			continue
		}
		fraction, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(fraction) || fraction < 0 || fraction > 1 {
			continue
		}
		used := fraction * 100
		window := UsageWindow{ID: item.id, WindowSeconds: item.seconds, UsedPercent: &used, CapturedAt: now}
		if reset, err := strconv.ParseInt(headers.Get("anthropic-ratelimit-unified-"+item.short+"-reset"), 10, 64); err == nil && reset > 0 {
			at := time.Unix(reset, 0)
			window.ResetsAt = &at
		}
		found := false
		for i := range s.Windows {
			if s.Windows[i].ID == item.id {
				s.Windows[i] = window
				found = true
				break
			}
		}
		if !found {
			s.Windows = append(s.Windows, window)
		}
		changed = true
	}
	if changed {
		_ = m.saveUsage(identity, s.At(now))
	}
}
