package webgui

import (
	"time"

	"supercli/internal/account/claudeauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

type subscriptionWindow struct {
	ID               string     `json:"id"`
	WindowSeconds    int64      `json:"window_seconds"`
	UsedPercent      *float64   `json:"used_percent"`
	RemainingPercent *float64   `json:"remaining_percent"`
	ResetsAt         *time.Time `json:"resets_at"`
	Stale            bool       `json:"stale"`
}

type subscriptionUsageView struct {
	Provider     string               `json:"provider"`
	ProviderType string               `json:"provider_type"`
	Account      string               `json:"account,omitempty"`
	CapturedAt   *time.Time           `json:"captured_at"`
	Windows      []subscriptionWindow `json:"windows"`
}

// These are saved account observations, not a sum or an API-price estimate.
// Rendering statistics must not start a quota request or token refresh.
func (e *Engine) subscriptionUsage() []subscriptionUsageView {
	out := []subscriptionUsageView{}
	accounts, _, _ := llm.ListCodexAccountUsageWithOptions(e.dataDir, e.providerManager().CodexAuthOptions())
	seen := map[string]bool{}
	for _, account := range accounts {
		if !account.LoggedIn {
			continue
		}
		if account.AccountID != "" {
			if seen[account.AccountID] {
				continue
			}
			seen[account.AccountID] = true
		}
		view := subscriptionUsageView{Provider: "ChatGPT", ProviderType: config.ProviderCodex, Account: account.Label, Windows: []subscriptionWindow{}}
		if account.Usage != nil {
			snapshot := account.Usage.At(time.Now())
			view.CapturedAt = snapshot.CapturedAt
			for _, limit := range snapshot.RateLimits {
				if limit.ID != "codex" {
					continue
				}
				for i, w := range []*llm.CodexUsageWindow{limit.Primary, limit.Secondary} {
					if w == nil {
						continue
					}
					window := subscriptionWindow{ID: []string{"primary", "secondary"}[i], UsedPercent: w.UsedPercent, RemainingPercent: w.RemainingPercent, Stale: w.Stale}
					if w.WindowSeconds != nil {
						window.WindowSeconds = *w.WindowSeconds
					}
					if w.ResetsAt != nil {
						at := time.Unix(*w.ResetsAt, 0)
						window.ResetsAt = &at
					}
					view.Windows = append(view.Windows, window)
				}
			}
		}
		out = append(out, view)
	}
	mgr := claudeauth.NewManager(e.dataDir, claudeauth.Options{})
	if mgr.LoggedIn() {
		view := subscriptionUsageView{Provider: "Claude", ProviderType: config.ProviderClaude, Windows: []subscriptionWindow{}}
		if usage := mgr.Usage(); usage != nil {
			view.CapturedAt = &usage.CapturedAt
			for _, w := range usage.Windows {
				view.Windows = append(view.Windows, subscriptionWindow{ID: w.ID, WindowSeconds: w.WindowSeconds, UsedPercent: w.UsedPercent, RemainingPercent: w.RemainingPercent, ResetsAt: w.ResetsAt, Stale: w.Stale})
			}
		}
		out = append(out, view)
	}
	return out
}
