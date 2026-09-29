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
	page := menuPage{title: m.tr("tui.menu_navigation.996c32b35f"), subtitle: m.tr("tui.menu_providers_render.7b5e636c3a"),
		footer: m.tr("tui.menu_providers_render.62a0abe49b"),
		empty:  m.tr("tui.menu_providers_render.a9e11383f5")}
	for i, p := range rows {
		name, typ := displayProvider(p.Name, p.Type)
		status, _ := m.providerStatusCell(p.Name)
		if p.Disabled {
			status = m.tr("tui.menu_providers_render.a7a9dc5bcf")
		}
		badge := ""
		if p.Name == active {
			badge = m.tr("tui.menu_models_render.a1922b55b9")
		}
		page.items = append(page.items, menuListItem{label: name, meta: status, badge: badge})
		if i == m.menu.cursor {
			enabled, total := m.providerModelCounts(p)
			keyState := m.tr("tui.menu_providers_render.b50f269f59")
			if p.HasKey {
				keyState = m.tr("tui.menu_providers_render.ea6e7dc79d")
			}
			page.detailTitle = name
			page.detail = []string{status, "", typ + " · " + keyState, fmt.Sprintf(m.tr("tui.menu_providers_render.ac6d2a2615"), enabled, total), p.Model, p.BaseURL, "",
				m.tr("tui.menu_providers_render.9d2559b425"), m.tr("tui.menu_providers_render.e15e3c0de1")}
			if m.cursorOnOpenAIRow() {
				page.detail = append(page.detail, m.tr("tui.menu_providers_render.4de8777abc"))
			}
			if st, ok := m.providerStatuses[p.Name]; ok && st.checked && !st.online && !p.Disabled {
				page.detail = append(page.detail, "", st.err)
			}
		}
	}
	page.items = append(page.items, menuListItem{
		label: m.tr("tui.menu_providers_render.b79ce9f971"),
		meta:  m.tr("tui.menu_providers_render.fd9fb8d616"),
	})
	if m.menu.cursor >= len(rows) {
		page.detailTitle = m.tr("tui.menu_providers_render.e1d36c3ade")
		page.detail = []string{m.tr("tui.menu_providers_render.af90f60a76"),
			"", "OpenAI · Anthropic · OpenCode Zen", "LM Studio · Ollama",
			"", m.tr("tui.menu_providers_render.b9d779bc3a")}
		page.footer = m.tr("tui.menu_providers_render.97662a7e11")
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
	p, ok := m.selectedConfiguredProvider()
	return ok && (p.Name == "openai" || p.Type == "codex")
}

// providerStatusCell returns the plain text and the styled text
// for the status column.
func (m Model) providerStatusCell(name string) (plain, styled string) {
	st, ok := m.providerStatuses[name]
	switch {
	case !ok || !st.checked:
		plain = m.tr("tui.menu_providers_render.7f98506ac7")
		styled = m.palette.InputHint.Render(plain)
	case st.online:
		plain = m.tr("tui.menu_providers_render.f6fc84c9f2")
		if st.latency > 0 {
			plain += " · " + formatProbeLatency(st.latency)
		}
		styled = m.palette.Success.Render(plain)
	default:
		plain = m.tr("tui.menu_providers_render.8e2c7ac508")
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
	labels := []string{m.tr("tui.menu_providers_render.dcd1d5223f"), m.tr("tui.menu_providers_render.baaddf70fb"), m.tr("tui.other.70589413a3"), m.tr("tui.menu_providers_render.16f0ee47f9"), m.tr("tui.menu_providers_render.3840d9d294")}
	title := m.tr("tui.menu_navigation.8cd1856b03")
	if m.menu.editName != "" {
		title = m.tr("tui.menu_providers_render.ba3a9d455b") + m.menu.editName
	}
	page := menuPage{title: title, footer: m.tr("tui.menu_providers_render.500226d704")}
	for i, label := range labels {
		value := ""
		if i < len(m.menu.form) {
			value = m.menu.form[i]
		}
		if i == 1 {
			value = "‹ " + m.providerProtocolLabel(value) + " ›"
		}
		// Use the field index, never its translated label, to mask credentials.
		if i == 3 && !(m.menu.formAt == 3 && m.menu.keyRevealed) {
			value = strings.Repeat("*", minInt(24, len([]rune(value))))
		}
		if i == m.menu.formAt {
			page.detailTitle = label
			page.detail = []string{value}
			if i == 1 {
				page.detail = []string{m.tr("tui.menu_providers_render.7b79076801"),
					"", m.tr("tui.menu_providers_render.fe86b310ec")}
				page.footer = m.tr("tui.menu_providers_render.0f8b44ba64")
			}
			if i == 3 {
				page.detail = []string{m.tr("tui.menu_providers_render.d0f000769b")}
				if m.menu.keyRevealed {
					page.detail = []string{m.tr("tui.menu_providers_render.e2900f3458")}
				}
				page.footer = m.tr("tui.menu_providers_render.e76f61a13c")
			}
			value += "▏"
		}
		page.items = append(page.items, menuListItem{label: label + ": " + value})
	}
	if m.menu.providerDetection != nil {
		page.footer = m.tr("tui.menu_providers_render.29735638e6")
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
	rows := m.providerTemplateRows()
	page := menuPage{title: m.tr("tui.menu_providers_render.e1d36c3ade"),
		subtitle:   m.tr("tui.menu_providers_render.ecb840d6e5"),
		searchable: true, footer: m.tr("tui.menu_providers_render.32112ffe0b")}
	for i, row := range rows {
		label := m.providerTemplateLabel(row.Name)
		page.items = append(page.items, menuListItem{label: label, meta: row.Desc})
		if i == m.menu.cursor {
			page.detailTitle = label
			page.detail = []string{row.Desc, "", row.BaseURL, "", m.providerProtocolLabel(row.Type)}
		}
	}
	return m.renderMenuPage(page)
}

func (m Model) renderOpenAIAuthMenu() string {
	page := menuPage{title: m.tr("tui.menu_providers_render.443b281cba"),
		footer: m.tr("tui.menu_providers_render.1a87e1edee")}
	page.items = []menuListItem{{label: m.tr("tui.menu_providers_render.3a477ce50c")}, {label: m.tr("tui.menu_providers_render.16f0ee47f9")}}
	page.detailTitle = page.items[minInt(m.menu.cursor, 1)].label
	if m.menu.cursor == 0 {
		page.detail = []string{m.tr("tui.menu_providers_render.eaec73252e")}
	} else {
		page.detail = []string{m.tr("tui.menu_providers_render.9c1a934db0")}
	}
	return m.renderMenuPage(page)
}
