package app

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
)

// buildCodexPool builds a Codex provider for every logged-in
// account and, when there is more than one, wraps them in a
// round-robin RouterProvider. Order is stable (ListAccounts sorts),
// default account first is not guaranteed — round-robin treats them
// equally, which is the point of spreading load across accounts.
func buildCodexPool(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	labels, err := codexauth.ListAccounts(dataDir)
	if err != nil {
		labels = nil // fall through to the default-account path
	}

	opts := codexauth.Options{}
	if codexAuthMgr != nil && filepath.Clean(filepath.Dir(codexAuthMgr.Path())) == filepath.Clean(dataDir) {
		opts = codexAuthMgr.Options()
	}
	if cfg.BaseURL != "" {
		opts.BackendURL = cfg.BaseURL
	}
	opts = opts.WithDefaults()
	// A single named account is still a valid login. Every manager uses the
	// same resolved endpoints, rather than reverting named accounts to defaults.
	var loggedIn []string
	var pool []llm.Provider
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, opts)
		if label == codexauth.DefaultAccount && codexAuthMgr != nil && codexAuthMgr.Path() == mgr.Path() && codexAuthMgr.Options() == opts {
			mgr = codexAuthMgr
		}
		if mgr.LoggedIn() {
			if available, known := providers.CodexAccountModelAvailability(dataDir, opts.BackendURL, mgr, cfg.Model); known && !available {
				continue
			}
			loggedIn = append(loggedIn, label)
			// Resolve the account id from disk (no network) so each
			// provider scopes its rate-limit snapshot to its own
			// account — otherwise both accounts share one file and
			// show the same usage.
			acctID := ""
			if info, e := mgr.Account(); e == nil {
				acctID = info.AccountID
			}
			p, err := llm.NewCodex(llm.CodexConfig{
				BackendURL:     mgr.Options().BackendURL,
				Model:          cfg.Model,
				Tokens:         mgr,
				Timeout:        cfg.Timeout,
				ConnectTimeout: cfg.ConnectTimeout,
				Capabilities:   caps,
				DataDir:        dataDir,
				AccountID:      acctID,
			})
			if err != nil {
				return nil, fmt.Errorf("buildCodexPool %q: %w", label, err)
			}
			pool = append(pool, p)
		}
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	if len(pool) > 1 {
		log.Printf("codex: round robin across %d accounts", len(pool))
		rt, err := llm.NewRouter(pool...)
		if err != nil {
			return nil, err
		}
		// Attach account labels so the HUD can show WHICH account is
		// active (e.g. "acct: drugie"), not just a slot number.
		rt.SetLabels(loggedIn)
		return rt, nil
	}

	// If saved logins all advertised the model absent, do not route it to
	// an unrelated default account. Unknown catalogs above remain eligible.
	for _, label := range labels {
		if codexauth.NewManagerFor(dataDir, label, opts).LoggedIn() {
			return nil, fmt.Errorf("codex: selected model is absent from the logged-in accounts' current catalogs")
		}
	}
	mgr := codexauth.NewManager(dataDir, opts)
	return llm.NewCodex(llm.CodexConfig{
		BackendURL:     mgr.Options().BackendURL,
		Model:          cfg.Model,
		Tokens:         mgr,
		Timeout:        cfg.Timeout,
		ConnectTimeout: cfg.ConnectTimeout,
		Capabilities:   caps,
		DataDir:        dataDir,
	})
}

// codexUsageFetcher is satisfied by *llm.CodexProvider. It lets the
// startup / model-swap hooks refresh the Codex rate-limit snapshot
// without importing the concrete type or caring whether the active
// provider is actually Codex.
type codexUsageFetcher interface {
	FetchUsage(ctx context.Context) (llm.CodexRateLimits, error)
}

// codexUsageAllFetcher is implemented by the multi-account router: it
// refreshes the usage snapshot for EVERY account in the pool (each with
// its own token), not just the active one. When a provider implements
// it, refreshing usage fills in every account's snapshot so the pool
// dashboard retains independent account limits. Single-account providers only
// implement codexUsageFetcher.
type codexUsageAllFetcher interface {
	FetchUsageAll(ctx context.Context) (llm.CodexRateLimits, error)
}

// refreshCodexUsage refreshes usage for all pooled accounts when prov
// is a multi-account router, otherwise just the active/only account.
// It returns the active account's snapshot and any (per-account)
// error, mirroring FetchUsage's signature so callers are unchanged.
func refreshCodexUsage(ctx context.Context, prov llm.Provider) (llm.CodexRateLimits, error) {
	prov = llm.Unwrap(prov)
	if fa, ok := prov.(codexUsageAllFetcher); ok {
		return fa.FetchUsageAll(ctx)
	}
	if f, ok := prov.(codexUsageFetcher); ok {
		return f.FetchUsage(ctx)
	}
	return llm.CodexRateLimits{}, fmt.Errorf("provider has no usage")
}

// codexPoolUsageDetail preserves each account's actual server windows.
func codexPoolUsageDetail(prov llm.Provider, languages ...string) string {
	language := optionalCommandLanguage(languages)
	rt, ok := llm.Unwrap(prov).(*llm.RouterProvider)
	if !ok {
		return ""
	}
	snaps, oks, active := rt.PoolUsage()
	if len(snaps) <= 1 {
		return ""
	}
	var b strings.Builder
	b.WriteString(uilang.Text(language, "app.usage.accounts"))
	for i, snapshot := range snaps {
		marker := "  "
		if i == active {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, rt.LabelAt(i))
		if !oks[i] || !snapshot.OK {
			b.WriteString("  " + uilang.Text(language, "acct.usage.noSnapshot") + "\n")
			continue
		}
		for _, line := range strings.Split(snapshot.FormatDetailFor(language), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	summary := rt.PoolUsageSummary()
	fmt.Fprintf(&b, "%s: %d · %s: %d · %s: %d · %s: %d\n", uilang.Text(language, "acct.usage.accounts"), summary.Accounts, uilang.Text(language, "acct.usage.available"), summary.Available, uilang.Text(language, "acct.usage.exhausted"), summary.Exhausted, uilang.Text(language, "acct.usage.unknown"), summary.Unknown)
	b.WriteString(uilang.Text(language, "acct.usage.aggregateHint"))
	return strings.TrimRight(b.String(), "\n")
}

// usageBar renders a used-percent as a 10-cell bar plus the number,
// e.g. 30 -> "▰▰▰▱▱▱▱▱▱▱ 30%". Clamped to 0..100.
func usageBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct + 5) / 10 // round to nearest cell
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled) +
		fmt.Sprintf(" %3d%%", pct)
}

// kickCodexUsageRefresh refreshes the Codex usage snapshot in the
// background when prov is a Codex provider. It is fire-and-forget and
// deliberately silent: a failure (offline, 401, non-Codex provider)
// leaves the last on-disk snapshot in place and never blocks the
// caller or surfaces an error to the user. The HUD reads the snapshot
// pull-style, so a successful refresh shows up on the next render.
//
// This is NOT a completion — it hits the dedicated usage endpoint and
// does not consume the quota the way /responses does.
//
// notify, when non-nil, is invoked after a SUCCESSFUL fetch so the
// caller can force a TUI redraw — the HUD `limit:` tile is pulled
// from the snapshot at render time, so without a redraw a swap onto a
// Codex model would not show fresh limits until the next keystroke.
func kickCodexUsageRefresh(prov llm.Provider, notify func()) {
	// Providers now arrive metered from the factory; peel the
	// decorator so the capability assertions see the transport.
	prov = llm.Unwrap(prov)
	// Accept either the single-account fetcher or the multi-account
	// router; refreshCodexUsage picks FetchUsageAll when available so
	// every pooled account gets fresh usage, not just the active one.
	_, single := prov.(codexUsageFetcher)
	_, all := prov.(codexUsageAllFetcher)
	if !single && !all {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// A per-account error (e.g. one expired token) is logged but
		// not fatal: accounts that succeeded still refreshed, so we
		// still redraw to show whatever fresh data we got.
		if _, err := refreshCodexUsage(ctx, prov); err != nil {
			log.Printf("codex usage refresh: %v", err)
		}
		if notify != nil {
			notify()
		}
	}()
}
