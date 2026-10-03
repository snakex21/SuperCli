package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
)

// Codex limits use observed account windows, independently of session tokens.
// Entry reads only the portable cache; provider requests require R or Enter.
type codexUsageMenuRow struct {
	menuListItem
	account string
	detail  []string
}

type codexUsageLoader func(context.Context, string, string, bool, bool, codexauth.Options) ([]llm.CodexAccountUsage, llm.CodexUsageSummary, error)

func loadCodexUsage(ctx context.Context, dataDir, label string, all, refresh bool, opts codexauth.Options) ([]llm.CodexAccountUsage, llm.CodexUsageSummary, error) {
	if refresh {
		return llm.RefreshCodexAccountUsageWithOptions(ctx, dataDir, label, all, opts)
	}
	return llm.ListCodexAccountUsageWithOptions(dataDir, opts)
}

func (m Model) beginCodexUsage(refresh bool) (tea.Model, tea.Cmd) {
	return m.beginCodexUsageWith(refresh, "", loadCodexUsage)
}

func (m Model) beginCodexUsageWith(refresh bool, label string, load codexUsageLoader) (tea.Model, tea.Cmd) {
	previous := m.menu.usage
	request := &usageSnapshot{scope: m.tr("acct.usage.title"), codexLoading: true}
	if previous != nil {
		request.codexAccounts, request.codexSummary = previous.codexAccounts, previous.codexSummary
	}
	m.menu.usage, m.menu.formErr = request, ""
	opts := codexauth.Options{}
	if m.providerMgr != nil {
		opts = m.providerMgr.CodexAuthOptions()
	}
	dataDir := m.dataDir
	// Capture small immutable request metadata, not Model/chat history. The
	// request pointer prevents a late result from replacing a newer menu.
	return m, func() tea.Msg {
		accounts, summary, err := load(context.Background(), dataDir, label, label == "", refresh, opts)
		result := &usageSnapshot{scope: request.scope, codexAccounts: accounts, codexSummary: summary}
		if err != nil {
			result.codexError = err.Error()
			if accounts == nil {
				result.codexAccounts, result.codexSummary = request.codexAccounts, request.codexSummary
			}
		}
		return usageLoadedMsg{data: result, scope: 2, request: request}
	}
}

func (m Model) openCodexUsageMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuUsage, category: 2})
	return m.beginCodexUsage(false)
}

func (m Model) refreshSelectedCodexUsage() (tea.Model, tea.Cmd) {
	if m.menu.usage == nil || m.menu.usage.codexLoading {
		return m, nil
	}
	rows := m.codexUsageRows()
	if len(rows) == 0 {
		return m, nil
	}
	label := rows[minInt(m.menu.cursor, len(rows)-1)].account
	if label == "" {
		return m, nil
	}
	return m.beginCodexUsageWith(true, label, loadCodexUsage)
}

func (m Model) codexWindowLabel(seconds *int64) string {
	if seconds == nil || *seconds <= 0 {
		return m.tr("acct.usage.windowUnknown")
	}
	n, key := *seconds, "acct.usage.durationSeconds"
	switch {
	case n%86400 == 0:
		n, key = n/86400, "acct.usage.durationDays"
	case n%3600 == 0:
		n, key = n/3600, "acct.usage.durationHours"
	case n%60 == 0:
		n, key = n/60, "acct.usage.durationMinutes"
	}
	duration := strings.ReplaceAll(m.tr(key), "{n}", fmt.Sprint(n))
	return strings.ReplaceAll(m.tr("acct.usage.period"), "{duration}", duration)
}

func codexUsagePercent(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "—"
	}
	return fmt.Sprintf("%g%%", math.Max(0, math.Min(100, *value)))
}

func codexUsageTime(at *time.Time) string {
	if at == nil || at.IsZero() {
		return "—"
	}
	return at.Local().Format("2006-01-02 15:04:05 MST")
}

func codexUsageReset(at *int64) string {
	if at == nil || *at <= 0 {
		return "—"
	}
	stamp := time.Unix(*at, 0)
	return codexUsageTime(&stamp)
}

func codexUsageAccountState(usage *llm.CodexUsageSnapshot, now time.Time) string {
	if usage == nil {
		return "unknown"
	}
	known, exhausted := usage.AvailabilityAt(now)
	if known {
		if exhausted {
			return "exhausted"
		}
		return "available"
	}
	if usage.At(now).Stale {
		return "stale"
	}
	return "unknown"
}

func (m Model) codexUsageRows() []codexUsageMenuRow {
	d := m.menu.usage
	if d == nil {
		return nil
	}
	if d.codexLoading && d.codexAccounts == nil {
		return nil
	}
	summary := d.codexSummary
	counts := []struct {
		key string
		n   int
	}{{"accounts", summary.Accounts}, {"available", summary.Available}, {"exhausted", summary.Exhausted}, {"unknown", summary.Unknown}, {"stale", summary.Stale}}
	var totals []string
	for _, count := range counts {
		totals = append(totals, m.tr("acct.usage."+count.key)+": "+fmt.Sprint(count.n))
	}
	rows := []codexUsageMenuRow{{menuListItem: menuListItem{label: m.tr("acct.usage.accounts"), badge: fmt.Sprint(summary.Accounts), meta: strings.Join(totals[1:], " · ")}, detail: append([]string{m.tr("acct.usage.aggregateHint")}, totals...)}}
	now := time.Now()
	loggedIn := 0
	for _, account := range d.codexAccounts {
		if !account.LoggedIn {
			continue
		}
		loggedIn++
		usage := account.Usage
		state := m.tr("acct.usage." + codexUsageAccountState(usage, now))
		plan := account.PlanType
		if usage != nil && usage.PlanType != "" {
			plan = usage.PlanType
		}
		meta := []string{account.Label + " · " + state}
		if account.Email != "" {
			meta = append(meta, account.Email)
		}
		if plan != "" {
			meta = append(meta, m.tr("acct.usage.plan")+": "+plan)
		}
		if usage != nil {
			meta = append(meta, strings.ReplaceAll(m.tr("acct.usage.captured"), "{time}", codexUsageTime(usage.CapturedAt)))
		}
		if account.UsageError != "" {
			meta = append(meta, account.UsageError)
		}
		rows = append(rows, codexUsageMenuRow{menuListItem: menuListItem{label: account.Label, badge: state, meta: strings.Join(meta[1:], " · ")}, account: account.Label, detail: meta})
		windows := 0
		if usage != nil {
			for _, limit := range usage.RateLimits {
				for _, window := range []*llm.CodexUsageWindow{limit.Primary, limit.Secondary} {
					if window == nil {
						continue
					}
					windows++
					label := m.codexWindowLabel(window.WindowSeconds)
					if limit.Name != "" {
						label = limit.Name + " · " + label
					} else if limit.ID != "" {
						label = limit.ID + " · " + label
					}
					metrics := m.tr("acct.usage.used") + " " + codexUsagePercent(window.UsedPercent) + " · " + m.tr("acct.usage.remaining") + " " + codexUsagePercent(window.RemainingPercent)
					reset := m.tr("acct.usage.reset") + ": " + codexUsageReset(window.ResetsAt)
					if window.Stale {
						reset += " · " + m.tr("acct.usage.stale")
					}
					detail := append(append([]string{}, meta...), label, metrics, reset)
					rows = append(rows, codexUsageMenuRow{menuListItem: menuListItem{label: label, badge: codexUsagePercent(window.UsedPercent), meta: account.Label + " · " + metrics + " · " + reset}, account: account.Label, detail: detail})
				}
			}
			if credits := usage.Credits; credits != nil {
				value := m.tr("acct.usage.unknown")
				switch {
				case credits.Unlimited != nil && *credits.Unlimited:
					value = m.tr("acct.usage.unlimited")
				case credits.Balance != nil && *credits.Balance != "":
					value = *credits.Balance
				case credits.HasCredits != nil && !*credits.HasCredits:
					value = m.tr("acct.usage.noCredits")
				}
				text := m.tr("acct.usage.credits") + ": " + value
				rows = append(rows, codexUsageMenuRow{menuListItem: menuListItem{label: m.tr("acct.usage.credits"), badge: value, meta: account.Label}, account: account.Label, detail: append(append([]string{}, meta...), text)})
			}
		}
		if windows == 0 {
			rows = append(rows, codexUsageMenuRow{menuListItem: menuListItem{label: m.tr("acct.usage.noSnapshot"), meta: account.Label}, account: account.Label, detail: meta})
		}
	}
	if loggedIn == 0 {
		rows = append(rows, codexUsageMenuRow{menuListItem: menuListItem{label: m.tr("acct.usage.noAccounts")}})
	}
	return rows
}

func (m Model) renderCodexUsageMenu() string {
	p := menuPage{title: m.tr("acct.usage.title"), subtitle: m.tr("acct.usage.aggregateHint"), footer: m.tr("acct.usage.refreshHint") + " · Enter " + m.tr("acct.usage.refresh"), empty: m.tr("acct.usage.noAccounts")}
	labels, selected := []string{m.tr("tui.menu_usage.34a04b78fc"), m.tr("acct.title")}, 1
	if m.loadedSessionID != "" {
		labels, selected = []string{labels[0], m.tr("tui.menu_usage.20844222e7"), labels[1]}, 2
	}
	p.tabs = m.menuTabs(labels, selected)
	rows := m.codexUsageRows()
	for _, row := range rows {
		p.items = append(p.items, row.menuListItem)
	}
	if len(rows) > 0 {
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		p.detailTitle, p.detail = row.label, row.detail
	}
	if d := m.menu.usage; d != nil {
		if d.codexLoading {
			p.subtitle = m.tr("common.loading") + " · " + p.subtitle
		}
		if d.codexError != "" {
			m.menu.formErr = d.codexError
		}
	}
	return m.renderMenuPage(p)
}
