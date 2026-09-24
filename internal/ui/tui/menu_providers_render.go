package tui

import (
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func (m Model) renderProvidersMenu() string {
	rows := m.providerRows()
	active := m.activeProviderName()
	page := menuPage{title: m.tr("Providers", "Dostawcy"), subtitle: m.tr("Connection status and active model", "Stan połączenia i aktywny model"),
		footer: m.tr("Enter models · A add · E edit · Space pause", "Enter modele · A dodaj · E edytuj · Space wstrzymaj"),
		empty:  m.tr("No providers configured — press A to add one.", "Brak dostawców — naciśnij A, aby dodać.")}
	for i, p := range rows {
		name, typ := displayProvider(p.Name, p.Type)
		status, _ := m.providerStatusCell(p.Name)
		if p.Disabled {
			status = m.tr("paused", "wstrzymany")
		}
		badge := ""
		if p.Name == active {
			badge = m.tr("● active", "● aktywny")
		}
		page.items = append(page.items, menuListItem{label: name, meta: status, badge: badge})
		if i == m.menu.cursor {
			enabled, total := m.providerModelCounts(p)
			keyState := m.tr("public/no key", "publiczny/bez klucza")
			if p.HasKey {
				keyState = m.tr("key configured", "klucz skonfigurowany")
			}
			page.detailTitle = name
			page.detail = []string{status, "", typ + " · " + keyState, fmt.Sprintf(m.tr("models %d/%d on", "modele włączone %d/%d"), enabled, total), p.Model, p.BaseURL, "",
				m.tr("R  Scan models", "R  Skanuj modele"), m.tr("D  Remove provider", "D  Usuń dostawcę")}
			if m.cursorOnOpenAIRow() {
				page.detail = append(page.detail, m.tr("C  ChatGPT accounts", "C  Konta ChatGPT"))
			}
			if st, ok := m.providerStatuses[p.Name]; ok && st.checked && !st.online && !p.Disabled {
				page.detail = append(page.detail, "", st.err)
			}
		}
	}
	return m.renderMenuPage(page)
}

func (m Model) providerModelCounts(p providers.ProviderInfo) (enabled, total int) {
	models := p.Models
	// A paused remote/local provider intentionally is not scanned, but its
	// already discovered catalog should remain visible in the summary.
	if len(models) == 0 && m.caps != nil {
		for _, model := range m.caps.All() {
			if model.Provider == p.Name && model.Source != llm.SourceSeed {
				models = append(models, model)
			}
		}
	}
	total = len(models)
	for _, model := range models {
		if m.providerMgr == nil || !m.providerMgr.IsHiddenFor(p.Name, model.ID) {
			enabled++
		}
	}
	return enabled, total
}

func (m Model) menuWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 120
}

// displayProvider maps internal provider entries to what the user
// should see: the legacy "codex" entry is just OpenAI signed in
// with a ChatGPT account.
func displayProvider(name, typ string) (string, string) {
	if typ == "codex" {
		return "openai", "chatgpt"
	}
	return name, typ
}

// cursorOnOpenAIRow reports whether the providers-menu cursor is on
// the OpenAI / ChatGPT row — the only row for which the ChatGPT
// accounts screen is relevant. Almost every provider has
// Type=="openai" (they are OpenAI-compatible), so we match on the
// NAME "openai" (or the legacy "codex" entry = OpenAI signed in
// with a ChatGPT account), not the type. Used to show the [C] hint
// and gate the 'c' shortcut contextually.
func (m Model) cursorOnOpenAIRow() bool {
	if m.menu.kind != menuProviders {
		return false
	}
	rows := m.providerRows()
	if len(rows) == 0 {
		return false
	}
	p := rows[minInt(m.menu.cursor, len(rows)-1)]
	return p.Name == "openai" || p.Type == "codex"
}

// providerStatusCell returns the plain text and the styled text
// for the status column.
func (m Model) providerStatusCell(name string) (plain, styled string) {
	st, ok := m.providerStatuses[name]
	switch {
	case !ok || !st.checked:
		plain = m.tr("checking", "sprawdzanie")
		styled = m.palette.InputHint.Render(plain)
	case st.online:
		plain = m.tr("online", "online")
		if st.latency > 0 {
			plain += " · " + formatProbeLatency(st.latency)
		}
		styled = m.palette.Success.Render(plain)
	default:
		plain = m.tr("offline", "offline")
		styled = m.palette.Error.Render(plain)
	}
	return plain, styled
}

func formatProbeLatency(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// activeProviderName returns the name of the provider that owns
// the currently loaded model, or empty string if unknown.
func (m Model) activeProviderName() string {
	if strings.TrimSpace(m.activeProvider) != "" {
		return m.activeProvider
	}
	if m.caps == nil {
		return ""
	}
	current := ""
	if m.modelSwapper != nil {
		current = m.modelSwapper.CurrentModel()
	}
	if current == "" && m.llm != nil {
		current = m.llm.Name()
	}
	if current == "" {
		return ""
	}
	for _, mi := range m.caps.All() {
		if mi.ID == current {
			return mi.Provider
		}
	}
	return ""
}

func (m Model) renderProviderForm() string {
	labels := []string{m.tr("Name", "Nazwa"), m.tr("Type", "Typ"), "Base URL", m.tr("API key", "Klucz API"), m.tr("Default model", "Domyślny model")}
	title := m.tr("Add provider", "Dodaj dostawcę")
	if m.menu.editName != "" {
		title = m.tr("Edit provider: ", "Edytuj dostawcę: ") + m.menu.editName
	}
	page := menuPage{title: title, footer: m.tr("↑↓ fields · Enter next/save · Ctrl+V paste", "↑↓ pola · Enter dalej/zapisz · Ctrl+V wklej")}
	for i, label := range labels {
		value := ""
		if i < len(m.menu.form) {
			value = m.menu.form[i]
		}
		// Use the field index, never its translated label, to mask credentials.
		if i == 3 && !(m.menu.formAt == 3 && m.menu.keyRevealed) {
			value = strings.Repeat("*", minInt(24, len([]rune(value))))
		}
		if i == m.menu.formAt {
			page.detailTitle = label
			page.detail = []string{value}
			if i == 3 {
				page.detail = []string{m.tr("The key stays hidden until you press →.", "Klucz pozostaje ukryty, dopóki nie naciśniesz →.")}
				if m.menu.keyRevealed {
					page.detail = []string{m.tr("← hides the key again.", "← ponownie ukrywa klucz.")}
				}
				page.footer = m.tr("← hide · → reveal · Enter next", "← ukryj · → pokaż · Enter dalej")
			}
			value += "▏"
		}
		page.items = append(page.items, menuListItem{label: label + ": " + value})
	}
	m.menu.cursor = m.menu.formAt
	return m.renderMenuPage(page)
}

// compactProviderError preserves the useful HTTP status/body while keeping a
// verbose upstream response from taking over the whole provider form.
func compactProviderError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	const maxRunes = 700
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}

func (m Model) renderPredefinedMenu() string {
	rows := providers.PredefinedProviders()
	page := menuPage{title: m.tr("Add provider — pick a template", "Dodaj dostawcę — wybierz szablon"),
		footer: m.tr("↑↓ choose · Enter pick", "↑↓ wybierz · Enter zatwierdź")}
	for i, row := range rows {
		page.items = append(page.items, menuListItem{label: row.Name})
		if i == m.menu.cursor {
			page.detailTitle = row.Name
			page.detail = []string{row.Desc, "", row.BaseURL}
		}
	}
	return m.renderMenuPage(page)
}

func (m Model) renderOpenAIAuthMenu() string {
	page := menuPage{title: m.tr("OpenAI — choose how to sign in", "OpenAI — wybierz sposób logowania"),
		footer: m.tr("↑↓ choose · Enter pick", "↑↓ wybierz · Enter zatwierdź")}
	page.items = []menuListItem{{label: m.tr("ChatGPT account", "Konto ChatGPT")}, {label: m.tr("API key", "Klucz API")}}
	page.detailTitle = page.items[minInt(m.menu.cursor, 1)].label
	if m.menu.cursor == 0 {
		page.detail = []string{m.tr("Sign in with your ChatGPT account (uses your subscription limits)", "Zaloguj konto ChatGPT (korzysta z limitów subskrypcji)")}
	} else {
		page.detail = []string{m.tr("API key (pay-as-you-go platform.openai.com key)", "Klucz API (płatność za użycie w platform.openai.com)")}
	}
	return m.renderMenuPage(page)
}
