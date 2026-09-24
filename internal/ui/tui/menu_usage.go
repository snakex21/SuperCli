package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/usagecost"
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
}
type usageLoadedMsg struct {
	data  *usageSnapshot
	err   error
	scope int
}

func (m Model) openUsageMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuUsage})
	return m, m.loadUsage()
}
func (m Model) loadUsage() tea.Cmd {
	selected := m.menu.category
	return func() tea.Msg {
		d := &usageSnapshot{sessionID: m.sessionID, scope: m.tr("Current CLI run", "Bieżące uruchomienie CLI")}
		if selected == 0 && m.statsRecorder == nil {
			return usageLoadedMsg{err: fmt.Errorf("%s", m.tr("Usage recording unavailable", "Statystyki są niedostępne")), scope: selected}
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
			d.scope = m.tr("Saved conversation", "Zapisana rozmowa")
			sess, err := m.sessionStore.Get(d.sessionID)
			if err != nil {
				return usageLoadedMsg{err: err, scope: selected}
			}
			d.model = sess.Model
			preview = identity(sess.Provider, sess.Model)
			var errUsage error
			records, errUsage = m.sessionStore.ReadUsage(context.Background(), d.sessionID)
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
				u := identity(c.Provider, c.Model)
				if u.Provider == "" {
					u = identity(m.activeProviderName(), c.Model)
				}
				u.Input = int64(c.TokensIn)
				u.Output = int64(c.TokensOut)
				u.CachedInput = int64(c.TokensCached)
				records = append(records, u)
			}
			// Older recorders may have turns but no measured provider calls.
			if len(records) == 0 {
				for _, t := range d.turns {
					u := preview
					u.Model = t.Model
					u.Input = int64(t.TokensIn)
					u.Output = int64(t.TokensOut)
					records = append(records, u)
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
		d.cost = usagecost.Resolve(tc, records, preview)
		return usageLoadedMsg{data: d, scope: selected}
	}
}
func (m Model) handleUsageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		if m.loadedSessionID != "" {
			m.menu.category = 1 - m.menu.category
			m.menu.cursor = 0
			m.menu.usage = nil
			return m, m.loadUsage()
		}
		return m, nil
	case "r", "R":
		return m, m.loadUsage()
	}
	return m.handleSearchMenuKey(key, func() int { return len(m.usageItems()) }, func() (tea.Model, tea.Cmd) { return m, nil })
}
func (m Model) costLabel(c usagecost.Summary) string {
	switch c.State {
	case "free":
		return m.tr("Free", "Bezpłatnie")
	case "local":
		return m.tr("Local model", "Model lokalny")
	case "subscription":
		return m.tr("Included in subscription", "W abonamencie")
	}
	if c.Amount == nil {
		return m.tr("Unknown price", "Brak ceny")
	}
	label := fmt.Sprintf("$%.6f", *c.Amount)
	if c.Estimated {
		label = "~" + label
	}
	if c.Partial {
		label += " " + m.tr("(partial)", "(częściowo)")
	}
	return label
}
func (m Model) usageItems() []menuListItem {
	d := m.menu.usage
	if d == nil {
		return nil
	}
	rows := []menuListItem{
		{label: m.tr("Total tokens", "Wszystkie tokeny"), badge: compactTokens(int(d.input + d.output)), meta: fmt.Sprintf("%d", d.input+d.output)},
		{label: m.tr("Input", "Wejście"), badge: compactTokens(int(d.input)), meta: fmt.Sprintf("%d", d.input)},
		{label: m.tr("Output", "Wyjście"), badge: compactTokens(int(d.output)), meta: fmt.Sprintf("%d", d.output)},
		{label: m.tr("Cached input (part of input)", "Cache (część wejścia)"), badge: compactTokens(int(d.cached)), meta: fmt.Sprintf("%d", d.cached)},
		{label: m.tr("Cost", "Koszt"), badge: m.costLabel(d.cost), meta: m.tr("Source: ", "Źródło: ") + d.cost.Source},
	}
	if d.reasoning > 0 {
		rows = append(rows, menuListItem{label: m.tr("Reasoning (part of output)", "Myślenie (część wyjścia)"), badge: compactTokens(int(d.reasoning))})
	}
	for _, c := range stats.SumCalls(d.calls) {
		meta := fmt.Sprintf(m.tr("%d in · %d out · %.2fs · errors %d", "%d wej. · %d wyj. · %.2fs · błędy %d"), c.TokensIn, c.TokensOut, float64(c.TotalUs)/1e6, c.Failed)
		if c.TTFTCount > 0 {
			meta += fmt.Sprintf(" · TTFT %.2fs", float64(c.TTFTUs)/float64(c.TTFTCount)/1e6)
		}
		rows = append(rows, menuListItem{label: m.tr("Model calls · ", "Wywołania · ") + c.Purpose, badge: fmt.Sprint(c.Count), meta: meta})
	}
	for _, t := range d.turns {
		rows = append(rows, menuListItem{label: fmt.Sprintf(m.tr("Step %d", "Krok %d"), t.Step), badge: fmt.Sprintf("%.2fs", float64(t.DurationMs)/1000), meta: fmt.Sprintf("%s · %d → %d · %s", t.Model, t.TokensIn, t.TokensOut, strings.Join(t.Tools, ", "))})
	}
	return rows
}
func (m Model) renderUsageMenu() string {
	p := menuPage{title: m.tr("Usage and costs", "Zużycie i koszty"), footer: m.tr("↑↓ details · R refresh", "↑↓ szczegóły · R odśwież"), empty: m.tr("Loading usage…", "Wczytywanie zużycia…")}
	p.tabs = m.menuTabs([]string{m.tr("Current run", "Bieżąca praca"), m.tr("Saved conversation", "Zapisana rozmowa")}, m.menu.category)
	if m.loadedSessionID == "" {
		p.tabs = m.menuTabs([]string{m.tr("Current run", "Bieżąca praca")}, 0)
	} else {
		p.footer += " · ←→ " + m.tr("scope", "zakres")
	}
	p.items = m.usageItems()
	if d := m.menu.usage; d != nil {
		p.subtitle = d.scope + " · " + d.model
		p.detailTitle = m.costLabel(d.cost)
		p.detail = []string{m.tr("Session: ", "Sesja: ") + d.sessionID, "", d.scope, m.tr("Model: ", "Model: ") + d.model, "", m.tr("Counts include recorded helper calls. Cache belongs to input; reasoning belongs to output.", "Liczby uwzględniają zarejestrowane wywołania pomocnicze. Cache jest częścią wejścia, a myślenie częścią wyjścia.")}
		if len(p.items) > 0 {
			row := p.items[minInt(m.menu.cursor, len(p.items)-1)]
			p.detail = append(p.detail, "", row.label, row.meta)
		}
		if d.cost.UnknownCalls > 0 {
			p.detail = append(p.detail, fmt.Sprintf(m.tr("Calls without a price: %d", "Wywołania bez znanej ceny: %d"), d.cost.UnknownCalls))
		}
	}
	return m.renderMenuPage(p)
}
