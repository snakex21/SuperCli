package llm

import (
	"context"
	"sync"
	"time"

	"supercli/internal/account/claudeauth"
	"supercli/internal/account/codexauth"
)

// Provider type names (config.ProviderCodex / config.ProviderClaude); config
// imports llm, so they are repeated here to avoid an import cycle.
const (
	subscriptionProviderCodex  = "codex"
	subscriptionProviderClaude = "claude"
)

const (
	// SubscriptionRefreshMinInterval bounds automatic quota requests per
	// account, successful or not, so a failing provider is never hammered.
	SubscriptionRefreshMinInterval = 60 * time.Second
	// SubscriptionRefreshMaxAge refreshes an otherwise valid snapshot while the
	// application stays open. Missing, stale or expired snapshots are due at once.
	SubscriptionRefreshMaxAge  = 5 * time.Minute
	subscriptionRefreshTimeout = 30 * time.Second
)

// SubscriptionUsageRefresher is a single-flight, per-account throttled
// background refresh of saved subscription quota snapshots (ChatGPT/Codex and
// Claude). Reading snapshots for display stays network-free; front-ends call
// RefreshDue asynchronously. Failures are silent: the last snapshot is kept
// and is presented as stale. The zero value is ready to use.
type SubscriptionUsageRefresher struct {
	mu      sync.Mutex
	running bool
	last    map[string]time.Time

	// Test seams; nil uses the wall clock and real account refreshes.
	Now    func() time.Time
	Claude func(ctx context.Context, dataDir string) error
	Codex  func(ctx context.Context, dataDir, label string, opts codexauth.Options) error
}

func (r *SubscriptionUsageRefresher) clock() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// allow records an attempt for key when it is outside the throttle window.
func (r *SubscriptionUsageRefresher) allow(key string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.last[key]; ok && !now.Before(last) && now.Sub(last) < SubscriptionRefreshMinInterval {
		return false
	}
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	r.last[key] = now
	return true
}

// SubscriptionRefreshDue reports whether a snapshot captured at captured needs
// a new request: missing, stale/expired, forced, or older than MaxAge.
func SubscriptionRefreshDue(captured *time.Time, stale, force bool, now time.Time) bool {
	if captured == nil || captured.IsZero() || stale || force {
		return true
	}
	return now.Sub(*captured) >= SubscriptionRefreshMaxAge || captured.After(now.Add(time.Minute))
}

// RefreshDue refreshes due snapshots of logged-in subscription accounts.
// forceProvider (config.ProviderCodex / config.ProviderClaude, e.g. after a
// finished chat run) refreshes that provider regardless of snapshot age, still
// throttled. busy reports that another refresh was already running.
func (r *SubscriptionUsageRefresher) RefreshDue(ctx context.Context, dataDir string, opts codexauth.Options, forceProvider string) (refreshed, busy bool) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return false, true
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, subscriptionRefreshTimeout)
	defer cancel()
	now := r.clock()

	refreshCodex := r.Codex
	if refreshCodex == nil {
		refreshCodex = func(ctx context.Context, dataDir, label string, opts codexauth.Options) error {
			_, _, err := RefreshCodexAccountUsageWithOptions(ctx, dataDir, label, false, opts)
			return err
		}
	}
	accounts, _, _ := ListCodexAccountUsageWithOptions(dataDir, opts)
	seen := map[string]bool{}
	for _, a := range accounts {
		if ctx.Err() != nil {
			break
		}
		// Unknown identity cannot be refreshed; aliases share one snapshot.
		if !a.LoggedIn || a.AccountID == "" || seen[a.AccountID] {
			continue
		}
		seen[a.AccountID] = true
		var captured *time.Time
		stale := false
		if a.Usage != nil {
			snapshot := a.Usage.At(now)
			captured, stale = snapshot.CapturedAt, snapshot.Stale
		}
		if !SubscriptionRefreshDue(captured, stale, forceProvider == subscriptionProviderCodex, now) || !r.allow("codex:"+a.AccountID, now) {
			continue
		}
		if refreshCodex(ctx, dataDir, a.Label, opts) == nil {
			refreshed = true
		}
	}

	mgr := claudeauth.NewManager(dataDir, claudeauth.Options{})
	if ctx.Err() == nil && mgr.LoggedIn() {
		var captured *time.Time
		stale := false
		if usage := mgr.Usage(); usage != nil {
			snapshot := usage.At(now)
			captured, stale = &snapshot.CapturedAt, snapshot.Stale
			for _, w := range snapshot.Windows {
				stale = stale || w.Stale
			}
		}
		if SubscriptionRefreshDue(captured, stale, forceProvider == subscriptionProviderClaude, now) && r.allow("claude", now) {
			refreshClaude := r.Claude
			if refreshClaude == nil {
				refreshClaude = func(ctx context.Context, _ string) error {
					_, err := mgr.RefreshUsage(ctx)
					return err
				}
			}
			if refreshClaude(ctx, dataDir) == nil {
				refreshed = true
			}
		}
	}
	return refreshed, false
}
