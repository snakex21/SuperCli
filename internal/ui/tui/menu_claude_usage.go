package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/claudeauth"
	"supercli/internal/llm"
)

func (m Model) beginClaudeUsage(refresh bool) (tea.Model, tea.Cmd) {
	request := &usageSnapshot{claudeLoading: true}
	if m.menu.usage != nil {
		request.claudeUsage = m.menu.usage.claudeUsage
	}
	m.menu.usage = request
	dataDir := m.dataDir
	return m, func() tea.Msg {
		mgr := claudeauth.NewManager(dataDir, claudeauth.Options{})
		out := &usageSnapshot{claudeUsage: mgr.Usage()}
		if refresh {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			usage, err := mgr.RefreshUsage(ctx)
			if err != nil {
				out.claudeError = err.Error()
			} else {
				out.claudeUsage = usage
			}
		}
		return usageLoadedMsg{data: out, scope: 3, request: request}
	}
}

func (m Model) claudeUsageItems() []menuListItem {
	if m.menu.usage == nil || m.menu.usage.claudeUsage == nil {
		return nil
	}
	var rows []menuListItem
	for _, w := range m.menu.usage.claudeUsage.At(time.Now()).Windows {
		seconds := w.WindowSeconds
		label := m.codexWindowLabel(&seconds)
		if w.ID == "seven_day_opus" {
			label = "Opus · " + label
		}
		if w.ID == "seven_day_sonnet" {
			label = "Sonnet · " + label
		}
		remaining := codexUsagePercent(w.RemainingPercent)
		meta := m.tr("acct.usage.used") + " " + codexUsagePercent(w.UsedPercent)
		if w.ResetsAt != nil {
			meta += " · " + m.tr("acct.usage.reset") + ": " + w.ResetsAt.Local().Format("2006-01-02 15:04")
		}
		if w.Stale {
			// An old snapshot whose window has not reset yet is still the best
			// known value; only an expired window has unknown usage.
			if w.ResetsAt != nil && !time.Now().Before(*w.ResetsAt) {
				remaining = "—"
			}
			meta += " · " + m.tr("acct.usage.stale")
		}
		rows = append(rows, menuListItem{label: label, badge: strings.ReplaceAll(m.tr("subscription.remaining"), "{value}", remaining), meta: meta})
	}
	return rows
}

func (m Model) renderClaudeUsageMenu() string {
	p := menuPage{title: m.tr("claude.usage.title"), empty: m.tr("acct.usage.noSnapshot"), footer: "R / Enter · " + m.tr("subscription.refresh") + " · ←→ ChatGPT / Claude"}
	labels := []string{m.tr("tui.menu_usage.34a04b78fc")}
	if m.loadedSessionID != "" {
		labels = append(labels, m.tr("tui.menu_usage.20844222e7"))
	}
	labels = append(labels, "ChatGPT", "Claude")
	p.tabs = m.menuTabs(labels, len(labels)-1)
	p.items = m.claudeUsageItems()
	if len(p.items) > 0 {
		row := p.items[minInt(m.menu.cursor, len(p.items)-1)]
		p.detailTitle = row.label
		p.detail = []string{row.badge, "", row.meta}
	}
	if d := m.menu.usage; d != nil {
		if d.claudeLoading {
			p.subtitle = m.tr("common.loading")
		}
		if d.claudeError != "" {
			m.menu.formErr = d.claudeError
		}
	}
	return m.renderMenuPage(p)
}

func (m Model) subscriptionRemainingLabel() string {
	var labels []string
	now := time.Now()
	staleMark := " (" + m.tr("acct.usage.stale") + ")"
	if accounts, _, err := llm.ListCodexAccountUsage(m.dataDir); err == nil {
		for _, a := range accounts {
			if !a.LoggedIn || a.Usage == nil {
				continue
			}
			for _, limit := range a.Usage.At(now).RateLimits {
				w := limit.Primary
				if limit.ID != "codex" || w == nil || w.RemainingPercent == nil || (w.ResetsAt != nil && *w.ResetsAt > 0 && now.Unix() >= *w.ResetsAt) {
					continue
				}
				label := "ChatGPT " + codexUsagePercent(w.RemainingPercent)
				if w.Stale {
					label += staleMark
				}
				labels = append(labels, label)
			}
		}
	}
	if usage := claudeauth.NewManager(m.dataDir, claudeauth.Options{}).Usage(); usage != nil {
		for _, w := range usage.Windows {
			if w.ID == "five_hour" && w.RemainingPercent != nil && (w.ResetsAt == nil || now.Before(*w.ResetsAt)) {
				label := "Claude " + fmt.Sprintf("%g%%", *w.RemainingPercent)
				if w.Stale {
					label += staleMark
				}
				labels = append(labels, label)
				break
			}
		}
	}
	if len(labels) == 0 {
		return m.tr("tui.menu_usage.8c7eadf499") + " · —"
	}
	return strings.ReplaceAll(m.tr("subscription.remaining"), "{value}", strings.Join(labels, " · "))
}
