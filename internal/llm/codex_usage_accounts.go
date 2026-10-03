package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
)

// CodexAccountUsage is shared by GUI and TUI. Loading it only reads portable
// account metadata and cached usage; it never exchanges tokens or sends HTTP.
type CodexAccountUsage struct {
	Label       string              `json:"label"`
	LoggedIn    bool                `json:"logged_in"`
	AccountID   string              `json:"account_id,omitempty"`
	Email       string              `json:"email,omitempty"`
	PlanType    string              `json:"plan_type,omitempty"`
	LastRefresh string              `json:"last_refresh,omitempty"`
	Usage       *CodexUsageSnapshot `json:"usage,omitempty"`
	UsageError  string              `json:"usage_error,omitempty"`
}

func ListCodexAccountUsage(dataDir string) ([]CodexAccountUsage, CodexUsageSummary, error) {
	return ListCodexAccountUsageWithOptions(dataDir, codexauth.Options{})
}

func ListCodexAccountUsageWithOptions(dataDir string, opts codexauth.Options) ([]CodexAccountUsage, CodexUsageSummary, error) {
	labels, err := codexauth.ListAccounts(dataDir)
	if err != nil {
		return nil, CodexUsageSummary{}, err
	}
	if len(labels) == 0 {
		labels = []string{codexauth.DefaultAccount}
	}
	accounts := make([]CodexAccountUsage, 0, len(labels))
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, opts)
		account := CodexAccountUsage{Label: label}
		if mgr.Label() != label {
			account.UsageError = "invalid account label"
			accounts = append(accounts, account)
			continue
		}
		info, err := mgr.Account()
		if err != nil {
			account.UsageError = "could not read account metadata"
		} else {
			account.LoggedIn, account.AccountID, account.Email, account.PlanType = info.LoggedIn, info.AccountID, info.Email, info.PlanType
			if !info.LastRefresh.IsZero() {
				account.LastRefresh = info.LastRefresh.Format(time.RFC3339)
			}
			// Unknown identity must not reuse the legacy shared snapshot.
			if info.LoggedIn && info.AccountID != "" {
				if usage, ok := LoadCodexUsageSnapshot(dataDir, info.AccountID); ok {
					account.Usage = &usage
					if usage.PlanType != "" {
						account.PlanType = usage.PlanType
					}
				}
			}
		}
		accounts = append(accounts, account)
	}
	return accounts, SummarizeCodexAccountUsage(accounts, time.Now()), nil
}

func SummarizeCodexAccountUsage(accounts []CodexAccountUsage, now time.Time) CodexUsageSummary {
	var summary CodexUsageSummary
	seen := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		if !account.LoggedIn {
			continue
		}
		// Aliases of one server identity do not create more capacity.
		if account.AccountID != "" {
			if seen[account.AccountID] {
				continue
			}
			seen[account.AccountID] = true
		}
		summary.add(account.Usage, now)
	}
	return summary
}

var codexManualUsageGate = make(chan struct{}, 1)

func acquireCodexUsageRefresh(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case codexManualUsageGate <- struct{}{}:
		return func() { <-codexManualUsageGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func RefreshCodexUsageSnapshot(ctx context.Context, dataDir, label string) (CodexUsageSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	release, err := acquireCodexUsageRefresh(ctx)
	if err != nil {
		return CodexUsageSnapshot{}, err
	}
	defer release()
	return refreshCodexUsageAccount(ctx, dataDir, label, codexauth.Options{})
}

func refreshCodexUsageAccount(ctx context.Context, dataDir, label string, opts codexauth.Options) (CodexUsageSnapshot, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		label = codexauth.DefaultAccount
	}
	mgr := codexauth.NewManagerFor(dataDir, label, opts)
	if mgr.Label() != label {
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: invalid account label")
	}
	info, err := mgr.Account()
	if err != nil || !info.LoggedIn {
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: account is not logged in")
	}
	if info.AccountID == "" {
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: account identity is unknown")
	}
	p, err := NewCodex(CodexConfig{Model: "usage-only", Tokens: mgr, BackendURL: mgr.Options().BackendURL, DataDir: dataDir, AccountID: info.AccountID})
	if err != nil {
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: could not initialize account usage")
	}
	defer p.http.CloseIdleConnections()
	rl, err := p.FetchUsage(ctx)
	if err != nil {
		return CodexUsageSnapshot{}, err
	}
	current, currentErr := mgr.Account()
	if currentErr != nil || !current.LoggedIn || current.AccountID != info.AccountID {
		_ = ClearCodexAccountRateLimits(dataDir, info.AccountID)
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: account changed during refresh")
	}
	if rl.Snapshot == nil {
		return CodexUsageSnapshot{}, fmt.Errorf("codex usage: no account usage snapshot")
	}
	return rl.Snapshot.At(time.Now()), nil
}

func RefreshCodexAccountUsage(ctx context.Context, dataDir, label string, all bool) ([]CodexAccountUsage, CodexUsageSummary, error) {
	return RefreshCodexAccountUsageWithOptions(ctx, dataDir, label, all, codexauth.Options{})
}

// RefreshCodexAccountUsageWithOptions performs serial, manual GETs bounded by
// one overall deadline. Successful accounts remain useful if another fails.
func RefreshCodexAccountUsageWithOptions(ctx context.Context, dataDir, label string, all bool, opts codexauth.Options) ([]CodexAccountUsage, CodexUsageSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := acquireCodexUsageRefresh(ctx)
	if err != nil {
		return nil, CodexUsageSummary{}, err
	}
	defer release()
	accounts, _, err := ListCodexAccountUsageWithOptions(dataDir, opts)
	if err != nil {
		return nil, CodexUsageSummary{}, err
	}
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		label = codexauth.DefaultAccount
	}
	found, failed := all, 0
	seen := make(map[string]CodexUsageSnapshot, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if !all && a.Label != label {
			continue
		}
		found = true
		if !a.LoggedIn {
			a.UsageError = "account is not logged in"
			failed++
			continue
		}
		if prior, ok := seen[a.AccountID]; ok && a.AccountID != "" {
			usage := prior.At(time.Now())
			a.Usage = &usage
			continue
		}
		usage, refreshErr := refreshCodexUsageAccount(ctx, dataDir, a.Label, opts)
		if refreshErr != nil {
			a.UsageError = safeCodexUsageError(refreshErr)
			failed++
			continue
		}
		a.Usage, a.UsageError = &usage, ""
		if usage.PlanType != "" {
			a.PlanType = usage.PlanType
		}
		seen[a.AccountID] = usage
	}
	// Logout/login may race a manual GET. Return current host-authored account
	// metadata, attaching successful snapshots only to the same live identity.
	if current, _, listErr := ListCodexAccountUsageWithOptions(dataDir, opts); listErr == nil {
		errors := make(map[string]string, len(accounts))
		for _, a := range accounts {
			errors[a.Label] = a.UsageError
		}
		for i := range current {
			current[i].UsageError = errors[current[i].Label]
			if current[i].LoggedIn {
				if usage, ok := seen[current[i].AccountID]; ok {
					copy := usage.At(time.Now())
					current[i].Usage = &copy
					if copy.PlanType != "" {
						current[i].PlanType = copy.PlanType
					}
				}
			}
		}
		accounts = current
	}
	if !found {
		return accounts, SummarizeCodexAccountUsage(accounts, time.Now()), fmt.Errorf("codex usage: account not found")
	}
	if failed > 0 {
		return accounts, SummarizeCodexAccountUsage(accounts, time.Now()), fmt.Errorf("codex usage: refresh failed for %d account(s)", failed)
	}
	return accounts, SummarizeCodexAccountUsage(accounts, time.Now()), nil
}

func safeCodexUsageError(err error) string {
	if err == context.DeadlineExceeded || strings.Contains(err.Error(), "context deadline exceeded") {
		return "usage refresh timed out"
	}
	if err == context.Canceled || strings.Contains(err.Error(), "context canceled") {
		return "usage refresh cancelled"
	}
	// Upstream/auth error bodies may contain credentials; they are never UI text.
	return "usage refresh failed"
}
