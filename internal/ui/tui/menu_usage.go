package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/fx"
	"supercli/internal/account/usagecost"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/stats"
)

type usageSnapshot struct {
	sessionID, model, scope          string
	input, output, cached, reasoning int64
	cost                             usagecost.Summary
	calls                            []stats.Call
	turns                            []stats.Turn
	codexAccounts                    []llm.CodexAccountUsage
	codexSummary                     llm.CodexUsageSummary
	codexLoading                     bool
	codexError                       string
}
type usageLoadedMsg struct {
	data    *usageSnapshot
	err     error
	scope   int
	request *usageSnapshot // guards Codex requests after close/reopen or tab changes
}

func (m Model) openUsageMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuUsage})
	return m, m.loadUsage()
}
func (m Model) loadUsage() tea.Cmd {
	selected := m.menu.category
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		d := &usageSnapshot{sessionID: m.sessionID, scope: m.tr("tui.menu_usage.fd8cbf3c80")}
		if selected == 0 && m.statsRecorder == nil {
			return usageLoadedMsg{err: fmt.Errorf("%s", m.tr("tui.menu_usage.864213086a")), scope: selected}
		}
		tc, _ := config.ResolveConfig(m.dataDir, m.home, "")
		identity := func(provider, model string) session.UsageRecord {
			u := session.UsageRecord{Provider: provider, Model: model}
			for _, p := range tc.Providers {
				if p.Name == provider {
					u.ProviderType = p.Type
					if base, e := url.Parse(p.BaseURL); e == nil {
						u.EndpointHost = base.Hostname()
					}
					break
				}
			}
			return u
		}
		if m.llm != nil {
			d.model = m.llm.Name()
		}
		preview := identity(m.activeProviderName(), d.model)
		var records []session.UsageRecord
		if selected == 1 && m.loadedSessionID != "" && m.sessionStore != nil {
			d.sessionID = m.loadedSessionID
			d.scope = m.tr("tui.menu_usage.20844222e7")
			sess, err := m.sessionStore.Get(d.sessionID)
			if err != nil {
				return usageLoadedMsg{err: err, scope: selected}
			}
			d.model = sess.Model
			preview = identity(sess.Provider, sess.Model)
			var errUsage error
			records, errUsage = m.sessionStore.ReadUsage(ctx, d.sessionID)
			if errUsage != nil {
				return usageLoadedMsg{err: errUsage, scope: selected}
			}
			if len(records) == 0 {
				preview.Input = int64(sess.TokenIn)
				preview.Output = int64(sess.TokenOut)
				d.input = preview.Input
				d.output = preview.Output
				if d.input+d.output > 0 {
					records = append(records, preview)
				}
			}
		} else if m.statsRecorder != nil {
			d.calls = m.statsRecorder.Calls()
			d.turns = m.statsRecorder.Snapshot()
			for _, c := range d.calls {
				provider := c.Provider
				if provider == "" {
					provider = m.activeProviderName()
				}
				u := usagecost.CallIdentity(tc, llm.CallStat{Model: c.Model, ProviderType: c.ProviderType,
					EndpointHost: c.EndpointHost, ConnectionKey: c.ConnectionKey, StartedAt: c.StartedAt}, identity(provider, c.Model))
				// An old injected recorder may lack a timestamp. Keep that unknown
				// rather than converting its history at the date of this menu read.
				u.CreatedAt, u.Source = c.StartedAt, c.Purpose
				u.Input = int64(c.TokensIn)
				u.Output = int64(c.TokensOut)
				u.CachedInput = int64(c.TokensCached)
				u.Reasoning = int64(c.TokensReasoning)
				u.HasCachedInput, u.HasReasoning = c.TokensCached > 0, c.TokensReasoning > 0
				records = append(records, u)
			}
			// Older recorders may have turns but no measured provider calls.
			if len(records) == 0 {
				for _, t := range d.turns {
					u := preview
					u.Model = t.Model
					u.CreatedAt = t.StartedAt
					u.Input = int64(t.TokensIn)
					u.Output = int64(t.TokensOut)
					records = append(records, u)
				}
			}
			if m.sessionStore != nil {
				if err := preferPersistedUsageCalls(ctx, m.sessionStore, records); err != nil {
					return usageLoadedMsg{err: err, scope: selected}
				}
			}
		}
		d.input, d.output = 0, 0
		for _, u := range records {
			d.input += u.Input
			d.output += u.Output
			d.cached += u.CachedInput
			d.reasoning += u.Reasoning
		}
		var cache *fx.Cache
		if config.EffectiveCostCurrency(tc) != "USD" {
			if m.usageRates != nil {
				cache, _ = m.usageRates.Cache()
			} else if m.dataDir != "" {
				cache, _ = fx.New(m.dataDir)
				if cache != nil {
					defer cache.Close()
				}
			}
			if cache != nil {
				_ = cache.Refresh(ctx)
			}
		}
		if len(records) == 0 {
			// Preserve the preview's classification without counting a model call.
			d.cost = usagecost.Resolve(tc, nil, preview)
			d.cost.Currency = config.EffectiveCostCurrency(tc)
		} else {
			cost := usagecost.NewCurrencyAccumulator(tc, cache)
			for _, u := range records {
				cost.Add(u)
			}
			d.cost = cost.Summary()
		}
		return usageLoadedMsg{data: d, scope: selected}
	}
}

// The current-process tab keeps its recorder's exact set of calls, including
// helpers and calls from earlier conversations in this process. Only matching
// journal entries replace fallback prices; older unrelated history is excluded.
func preferPersistedUsageCalls(ctx context.Context, store *session.Store, records []session.UsageRecord) error {
	type callKey struct {
		started                          int64
		model, source                    string
		input, output, cached, reasoning int64
	}
	key := func(u session.UsageRecord) callKey {
		source := u.Source
		if source == "" {
			source = "model"
		}
		input, output := max(u.Input, 0), max(u.Output, 0)
		return callKey{u.CreatedAt.UnixNano(), u.Model, source, input, output,
			min(max(u.CachedInput, 0), input), min(max(u.Reasoning, 0), output)}
	}
	var since time.Time
	wanted := make(map[callKey][]int)
	for i, u := range records {
		if u.CreatedAt.IsZero() {
			continue
		}
		if since.IsZero() || u.CreatedAt.Before(since) {
			since = u.CreatedAt
		}
		wanted[key(u)] = append(wanted[key(u)], i)
	}
	if since.IsZero() {
		return nil
	}
	return store.VisitBilling(ctx, "", since, func(u session.UsageRecord) {
		k := key(u)
		for n, i := range wanted[k] {
			candidate := records[i]
			if candidate.ProviderType != "" && candidate.ProviderType != u.ProviderType {
				continue
			}
			if candidate.EndpointHost != "" && candidate.EndpointHost != u.EndpointHost {
				continue
			}
			records[i] = u
			wanted[k] = append(wanted[k][:n], wanted[k][n+1:]...)
			return
		}
	})
}
func (m Model) handleUsageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		categories := []int{0, 2}
		if m.loadedSessionID != "" {
			categories = []int{0, 1, 2}
		}
		index := 0
		for i, c := range categories {
			if c == m.menu.category {
				index = i
				break
			}
		}
		step := 1
		if key.String() == "left" || key.String() == "shift+tab" {
			step = -1
		}
		m.menu.category = categories[(index+step+len(categories))%len(categories)]
		m.menu.cursor = 0
		m.menu.usage = nil
		m.menu.formErr = ""
		if m.menu.category == 2 {
			return m.beginCodexUsage(false)
		}
		return m, m.loadUsage()
	case "r", "R":
		if m.menu.category == 2 {
			if m.menu.usage != nil && m.menu.usage.codexLoading {
				return m, nil
			}
			return m.beginCodexUsage(true)
		}
		return m, m.loadUsage()
	}
	return m.handleSearchMenuKey(key, func() int { return len(m.usageItems()) }, func() (tea.Model, tea.Cmd) {
		if m.menu.category == 2 {
			return m.refreshSelectedCodexUsage()
		}
		return m, nil
	})
}

func (m Model) costLabel(c usagecost.Summary) string {
	switch c.State {
	case "free":
		return m.tr("tui.menu_usage.f411a1fb62")
	case "local":
		return m.tr("tui.menu_usage.ea815782df")
	case "subscription":
		return m.tr("tui.menu_usage.8c7eadf499")
	}
	if c.Amount == nil {
		if c.MissingFXCalls > 0 {
			return m.tr("tui.menu_usage.7311518410") + " (" + c.Currency + ")"
		}
		return m.tr("tui.menu_usage.7311518410")
	}
	currency := c.Currency
	if currency == "" {
		currency = "USD"
	}
	label := fmt.Sprintf("%.6f %s", *c.Amount, currency)
	if c.Estimated {
		label = "~" + label
	}
	if c.Partial {
		label += " " + m.tr("tui.menu_usage.d0ffde3448")
	}
	return label
}
func (m Model) usageItems() []menuListItem {
	if m.menu.category == 2 {
		rows := m.codexUsageRows()
		items := make([]menuListItem, len(rows))
		for i, row := range rows {
			items[i] = row.menuListItem
		}
		return items
	}
	d := m.menu.usage
	if d == nil {
		return nil
	}
	rows := []menuListItem{
		{label: m.tr("tui.menu_usage.e7601ca117"), badge: compactTokens(int(d.input + d.output)), meta: fmt.Sprintf("%d", d.input+d.output)},
		{label: m.tr("tui.menu_usage.36ecb4f866"), badge: compactTokens(int(d.input)), meta: fmt.Sprintf("%d", d.input)},
		{label: m.tr("tui.menu_usage.b2439bcb8d"), badge: compactTokens(int(d.output)), meta: fmt.Sprintf("%d", d.output)},
		{label: m.tr("tui.menu_usage.c782217680"), badge: compactTokens(int(d.cached)), meta: fmt.Sprintf("%d", d.cached)},
		{label: m.tr("tui.menu_usage.204a5eb2cd"), badge: m.costLabel(d.cost), meta: m.tr("tui.menu_settings_render.1a5ac0bd0b") + m.costSourceLabel(d.cost.Source)},
	}
	if d.reasoning > 0 {
		rows = append(rows, menuListItem{label: m.tr("tui.menu_usage.dea938851c"), badge: compactTokens(int(d.reasoning))})
	}
	for _, c := range stats.SumCalls(d.calls) {
		meta := fmt.Sprintf(m.tr("tui.menu_usage.f176686d37"), c.TokensIn, c.TokensOut, float64(c.TotalUs)/1e6, c.Failed)
		if c.TTFTCount > 0 {
			meta += fmt.Sprintf(m.tr("tui.menu_usage.3689bd94c8"), float64(c.TTFTUs)/float64(c.TTFTCount)/1e6)
		}
		rows = append(rows, menuListItem{label: m.tr("tui.menu_usage.4148e8f65d") + c.Purpose, badge: fmt.Sprint(c.Count), meta: meta})
	}
	for _, t := range d.turns {
		rows = append(rows, menuListItem{label: fmt.Sprintf(m.tr("tui.menu_usage.10151a408b"), t.Step), badge: fmt.Sprintf("%.2fs", float64(t.DurationMs)/1000), meta: fmt.Sprintf("%s · %d → %d · %s", t.Model, t.TokensIn, t.TokensOut, strings.Join(t.Tools, ", "))})
	}
	return rows
}

func (m Model) costSourceLabel(source string) string {
	switch source {
	case "manual", "free", "local", "subscription", "provider", "official", "catalog", "mixed":
		return m.tr("tui.cost_source." + source)
	case "", "fx_missing":
		return m.tr("tui.menu_usage.7311518410")
	default:
		return source
	}
}
func (m Model) renderUsageMenu() string {
	if m.menu.category == 2 {
		return m.renderCodexUsageMenu()
	}
	p := menuPage{title: m.tr("tui.menu_usage.6ed5b1520e"), footer: m.tr("tui.menu_usage.6dc0164c79"), empty: m.tr("tui.menu_usage.0134e99d31")}
	p.tabs = m.menuTabs([]string{m.tr("tui.menu_usage.34a04b78fc"), m.tr("tui.menu_usage.20844222e7"), m.tr("acct.title")}, m.menu.category)
	if m.loadedSessionID == "" {
		p.tabs = m.menuTabs([]string{m.tr("tui.menu_usage.34a04b78fc"), m.tr("acct.title")}, 0)
	} else {
		p.footer += " · ←→ " + m.tr("tui.menu_usage.5f161c9149")
	}
	p.items = m.usageItems()
	if d := m.menu.usage; d != nil {
		p.subtitle = d.scope + " · " + d.model
		p.detailTitle = m.costLabel(d.cost)
		p.detail = []string{m.tr("tui.menu_usage.75cccf69c1") + d.sessionID, "", d.scope, m.tr("tui.actions_render.33f4e5313c") + d.model, "", m.tr("tui.menu_usage.d1b25c7a2e")}
		if len(p.items) > 0 {
			row := p.items[minInt(m.menu.cursor, len(p.items)-1)]
			p.detail = append(p.detail, "", row.label, row.meta)
		}
		if d.cost.UnknownCalls > 0 {
			p.detail = append(p.detail, fmt.Sprintf(m.tr("tui.menu_usage.000bbe6c92"), d.cost.UnknownCalls))
		}
	}
	return m.renderMenuPage(p)
}
