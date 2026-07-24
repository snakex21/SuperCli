package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
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

	// Count accounts that actually have usable tokens. Only a
	// genuine multi-account setup (>=2) takes the router path; a
	// single account uses the original global-manager path
	// unchanged (preserving its usage snapshot / HUD behaviour).
	var loggedIn []string
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
		if mgr.LoggedIn() {
			loggedIn = append(loggedIn, label)
		}
	}

	if len(loggedIn) > 1 {
		var pool []llm.Provider
		for _, label := range loggedIn {
			mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
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
		log.Printf("codex: magazine across %d accounts: %v", len(pool), loggedIn)
		rt, err := llm.NewRouter(pool...)
		if err != nil {
			return nil, err
		}
		// Attach account labels so the HUD can show WHICH account is
		// active (e.g. "acct: drugie"), not just a slot number.
		rt.SetLabels(loggedIn)
		return rt, nil
	}

	// Single (or zero) account: preserve the exact original path,
	// including the global codexAuthMgr the /login command already
	// populated (carries the usage snapshot for the HUD).
	mgr := codexAuthMgr
	if mgr == nil {
		mgr = codexauth.NewManager(dataDir, codexauth.Options{})
	}
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
// aggregate counts all accounts — the whole point of the magazine
// being one combined limit. Single-account / non-router providers only
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

// codexPoolUsageDetail returns a per-account usage breakdown when
// prov is a multi-account router, or "" otherwise. It renders an
// aligned table with a small bar for each account's 5h and 7d
// usage, marks the active account, and adds a pool total row — so
// the user sees both "this account" and "all accounts combined".
func codexPoolUsageDetail(prov llm.Provider) string {
	rt, ok := llm.Unwrap(prov).(*llm.RouterProvider)
	if !ok {
		return ""
	}
	snaps, oks, active := rt.PoolUsage()
	if len(snaps) <= 1 {
		return "" // single account: the main detail already covers it
	}
	// Column width: longest account label (so the bars line up).
	nameW := len("account")
	for i := range snaps {
		if l := len(rt.LabelAt(i)); l > nameW {
			nameW = l
		}
	}
	var b strings.Builder
	b.WriteString("\n\naccounts (magazine — active drains first):\n")
	for i, s := range snaps {
		marker := "  "
		if i == active {
			marker = "▶ "
		}
		name := rt.LabelAt(i)
		if !oks[i] || !s.OK {
			fmt.Fprintf(&b, "%s%-*s   (no usage data yet)\n", marker, nameW, name)
			continue
		}
		fmt.Fprintf(&b, "%s%-*s   5h %s   7d %s\n",
			marker, nameW, name,
			usageBar(s.PrimaryUsedPct), usageBar(s.SecondaryUsedPct))
	}
	// Pool total.
	if p5, p7, n := rt.PoolAggregate(); n > 0 {
		fmt.Fprintf(&b, "  %-*s   5h %s   7d %s\n",
			nameW, "POOL", usageBar(p5), usageBar(p7))
	}
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
