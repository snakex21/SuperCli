package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func (m Model) renderProvidersMenu() string {
	rows := m.providerRows()
	active := m.activeProviderName()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(fmt.Sprintf(m.tr("Providers · %d", "Dostawcy · %d"), len(rows))) + "\n")
	b.WriteString(m.palette.InputHint.Render(m.tr("Connection status and active model", "Stan połączenia i aktywny model")) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		available := (m.height - 5) / 2
		start, end = menuWindow(len(rows), m.menu.cursor, available)
	}
	for i := start; i < end; i++ {
		p := rows[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		activeText := ""
		if p.Name == active {
			activeText = m.tr(" [active]", " [aktywny]")
		}
		model := p.Model
		if model == "" {
			model = "-"
		}
		name, typ := displayProvider(p.Name, p.Type)
		statusText, statusStyled := m.providerStatusCell(p.Name)
		if p.Disabled {
			statusText = m.tr("paused", "wstrzymany")
			statusStyled = m.palette.InputHint.Render(statusText)
		}
		plainLine := truncateText(prefix+name+activeText+" · "+statusText, width)
		line := prefix + name + activeText + " · " + statusStyled
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(plainLine)
		} else {
			plainPrefix := truncateText(prefix+name+activeText+" · ", maxInt(4, width-lipgloss.Width(statusText)))
			line = m.palette.Bold.Render(plainPrefix) + statusStyled
		}
		b.WriteString(line + "\n")
		enabled, total := m.providerModelCounts(p)
		modelState := m.tr("models not scanned", "modele nieskanowane")
		if total > 0 {
			modelState = fmt.Sprintf(m.tr("models %d/%d on", "modele włączone %d/%d"), enabled, total)
		}
		keyState := m.tr("public/no key", "publiczny/bez klucza")
		if p.HasKey {
			keyState = m.tr("key configured", "klucz skonfigurowany")
		}
		meta := "    " + typ + " · " + modelState + " · " + keyState
		if model != "-" {
			meta += m.tr(" · default ", " · domyślny ") + model
		}
		if p.BaseURL != "" {
			meta += " · " + p.BaseURL
		}
		if st, ok := m.providerStatuses[p.Name]; ok && !p.Disabled && st.checked && !st.online && st.err != "" {
			meta += " · " + st.err
		}
		b.WriteString(m.palette.Dim.Render(truncateText(meta, width)) + "\n")
	}
	if len(rows) == 0 {
		b.WriteString("  " + m.tr("no providers configured — press A to add one", "brak dostawców — naciśnij A, aby dodać") + "\n")
	}
	hint := m.tr("↑↓ select · Enter models · Space pause/resume · A add · E edit · D delete · R scan", "↑↓ wybierz · Enter modele · Space wstrzymaj/wznów · A dodaj · E edytuj · D usuń · R skanuj")
	if m.cursorOnOpenAIRow() {
		hint += m.tr(" · C ChatGPT accounts", " · C konta ChatGPT")
	}
	hint += m.tr(" · Esc back", " · Esc wróć")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
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
	labels := []string{m.tr("name", "nazwa"), m.tr("type", "typ"), "base URL", m.tr("API key", "klucz API"), m.tr("default model", "domyślny model")}
	width := m.menuWidth()
	var b strings.Builder
	title := m.tr("Add provider", "Dodaj dostawcę")
	if m.menu.editName != "" {
		title = m.tr("Edit provider: ", "Edytuj dostawcę: ") + m.menu.editName
	}
	b.WriteString(m.palette.PanelTitle.Render(title) + "\n\n")
	if m.menu.formErr != "" {
		for _, line := range wrap(m.menu.formErr, maxInt(24, width-2)) {
			b.WriteString(m.palette.Error.Render(truncateText("! "+line, width)) + "\n")
		}
		b.WriteString("\n")
	}
	for i, label := range labels {
		prefix := "  "
		if i == m.menu.formAt {
			prefix = "> "
		}
		value := ""
		if i < len(m.menu.form) {
			value = m.menu.form[i]
		}
		if label == "API key" && value != "" {
			if m.menu.formAt == 3 && m.menu.keyRevealed {
				// On API key field + right arrow pressed → show real key.
			} else {
				value = strings.Repeat("*", len([]rune(value)))
			}
		}
		line := truncateText(fmt.Sprintf("%s%-9s %s", prefix, label+":", value), width)
		if i == m.menu.formAt {
			line = m.palette.HeaderMode.Render(line)
		}
		b.WriteString(line + "\n")
	}
	hint := m.tr("type/paste · Ctrl+V paste · Enter next/save · ↑↓ fields · Esc back", "pisz/wklej · Ctrl+V wklej · Enter dalej/zapisz · ↑↓ pola · Esc wróć")
	if m.menu.formAt == 3 {
		if m.menu.keyRevealed {
			hint = m.tr("← hide key · type/paste · Ctrl+V paste · Enter save · Esc back", "← ukryj klucz · pisz/wklej · Ctrl+V wklej · Enter zapisz · Esc wróć")
		} else {
			hint = m.tr("→ reveal key · type/paste · Ctrl+V paste · Enter save · Esc back", "→ pokaż klucz · pisz/wklej · Ctrl+V wklej · Enter zapisz · Esc wróć")
		}
	} else if m.menu.formAt == 4 {
		hint = m.tr("optional · keeps an offline model in /model · Enter save · Esc back", "opcjonalne · zachowuje model offline na liście · Enter zapisz · Esc wróć")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
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
	pres := providers.PredefinedProviders()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Add provider — pick a template", "Dodaj dostawcę — wybierz szablon")) + "\n\n")
	start, end := 0, len(pres)
	if m.height > 0 {
		start, end = menuWindow(len(pres), m.menu.cursor, (m.height-5)/2)
	}
	for i := start; i < end; i++ {
		p := pres[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		line := truncateText(p.Name+" · "+p.Desc, width-2)
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(prefix + line)
		} else {
			line = prefix + m.palette.Bold.Render(line)
		}
		b.WriteString(line + "\n")
		b.WriteString(m.palette.Dim.Render(truncateText("    "+p.BaseURL, width)) + "\n")
	}
	if len(pres) == 0 {
		b.WriteString("  " + m.tr("no predefined providers", "brak gotowych dostawców") + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("↑↓ select · Enter pick · Esc back", "↑↓ wybierz · Enter zatwierdź · Esc wróć"), width)))
	return b.String()
}

func (m Model) renderOpenAIAuthMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("OpenAI — choose how to sign in", "OpenAI — wybierz sposób logowania")) + "\n\n")
	opts := []string{
		m.tr("Sign in with your ChatGPT account (uses your subscription limits)", "Zaloguj konto ChatGPT (korzysta z limitów subskrypcji)"),
		m.tr("API key (pay-as-you-go platform.openai.com key)", "Klucz API (płatność za użycie w platform.openai.com)"),
	}
	for i, o := range opts {
		prefix := "  "
		line := truncateText(o, width-2)
		if i == m.menu.cursor {
			prefix = "> "
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Dim.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("↑↓ select · Enter pick · Esc back", "↑↓ wybierz · Enter zatwierdź · Esc wróć"), width)))
	return b.String()
}
