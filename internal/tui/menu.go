package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/goal"
	"supercli/internal/llm"
	"supercli/internal/providers"
)

type menuKind int

const (
	menuNone menuKind = iota
	menuModels
	menuProviders
	menuProviderModels
	menuProviderForm
	menuProviderPredefined
	menuGoal
)

type interactiveMenu struct {
	kind        menuKind
	cursor      int
	filter      string
	provider    string
	form        []string
	formAt      int
	editName    string
	keyRevealed bool // true = API key shown in plain text
}

func (m Model) openModelsMenu() (tea.Model, tea.Cmd) {
	// Scan providers only if the registry is empty
	// (models haven't been fetched yet, e.g. before the
	// background startup scan completes). Otherwise the
	// background scan keeps the registry up to date.
	if m.providerMgr != nil && m.caps != nil && len(m.caps.All()) == 0 {
		m.providerMgr.ScanModels(m.caps)
	}
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuModels}
	m.input.Blur()
	return m, nil
}

func (m Model) openProvidersMenu() (tea.Model, tea.Cmd) {
	if m.providerMgr != nil {
		m.providerMgr.Reload()
	}
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviders}
	m.input.Blur()
	return m, nil
}

func (m Model) openGoalMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuGoal}
	m.input.Blur()
	return m, nil
}

func (m Model) closeMenu() (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	return m, nil
}

func (m Model) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// In the provider form, most keys are text input — handle only
	// navigation/special keys here, everything else falls to rune handler.
	if m.menu.kind == menuProviderForm {
		return m.handleFormKey(msg)
	}

	key := msg.String()
	lowerKey := strings.ToLower(key)
	switch lowerKey {
	case "esc":
		return m.closeMenu()
	case "up", "k":
		// Only navigate if no filter is active. When filtering,
		// 'k'/'j' are regular characters.
		if m.menu.filter != "" && (lowerKey == "k" || lowerKey == "j") {
			break // fall through to rune handler
		}
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down", "j":
		if m.menu.filter != "" && lowerKey == "j" {
			break
		}
		m.menu.cursor++
		m.clampMenuCursor()
		return m, nil
	case "backspace", "ctrl+h":
		if m.menu.filter != "" {
			r := []rune(m.menu.filter)
			m.menu.filter = string(r[:len(r)-1])
			m.menu.cursor = 0
		}
		return m, nil
	case "enter":
		return m.menuEnter()
	case " ":
		return m.menuSpace()
	case "right":
		// Right arrow: enable/show model.
		if (m.menu.kind == menuModels || m.menu.kind == menuProviderModels) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				m.providerMgr.ShowModel(rows[minInt(m.menu.cursor, len(rows)-1)].ID)
			}
		}
		return m, nil
	case "left":
		// Left arrow: disable/hide model.
		if (m.menu.kind == menuModels || m.menu.kind == menuProviderModels) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				m.providerMgr.HideModel(rows[minInt(m.menu.cursor, len(rows)-1)].ID)
			}
		}
		return m, nil
	case "a":
		if m.menu.kind == menuProviders {
			m.menu = interactiveMenu{kind: menuProviderPredefined}
			m.input.Blur()
			return m, nil
		}
		if m.menu.kind == menuGoal && m.goalSvc != nil {
			_, err := m.goalSvc.AddTask(context.Background(), "", "new task")
			if err != nil {
				m.appendLine(m.marker.Error(err))
			}
		}
		return m, nil
	case "e":
		if m.menu.kind == menuProviders {
			rows := m.providerRows()
			if len(rows) > 0 {
				p := rows[minInt(m.menu.cursor, len(rows)-1)]
				m.menu = interactiveMenu{kind: menuProviderForm, editName: p.Name, form: []string{p.Name, p.Type, p.BaseURL, ""}}
				m.input.Blur()
			}
		}
		return m, nil
	case "d":
		if m.menu.kind == menuProviders && m.providerMgr != nil {
			rows := m.providerRows()
			if len(rows) > 0 {
				_ = m.providerMgr.Remove(rows[minInt(m.menu.cursor, len(rows)-1)].Name)
				m.providerMgr.Reload()
				m.menu.cursor = 0
			}
		}
		return m, nil
	case "r":
		if m.menu.kind == menuProviders && m.providerMgr != nil {
			m.providerMgr.Reload()
		}
		return m, nil
	case "m":
		if m.menu.kind == menuProviders {
			providers := m.providerRows()
			if len(providers) > 0 {
				idx := minInt(m.menu.cursor, len(providers)-1)
				// Scan this provider's models before showing them.
				if m.providerMgr != nil && m.caps != nil {
					m.providerMgr.ScanModels(m.caps)
				}
				m.menu = interactiveMenu{kind: menuProviderModels, provider: providers[idx].Name}
			}
		}
		return m, nil
	}
	if len(msg.Runes) > 0 && (m.menu.kind == menuModels || m.menu.kind == menuProviderModels) {
		// Only add printable characters to the filter.
		// Skip control characters and Alt/Meta sequences.
		for _, r := range msg.Runes {
			if r >= ' ' && r <= '~' {
				m.menu.filter += string(r)
			}
		}
		m.menu.cursor = 0
	}
	return m, nil
}

// handleFormKey handles keys when editing the provider form.
// Only navigation and special keys are intercepted; every other
// key (including letters like a, d, e, r, m) goes to text input.
func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeMenu()
	case "up":
		m.menu.keyRevealed = false
		if m.menu.formAt > 0 {
			m.menu.formAt--
		}
		return m, nil
	case "down":
		m.menu.keyRevealed = false
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
		}
		return m, nil
	case "right":
		if m.menu.formAt == 3 {
			m.menu.keyRevealed = true
		}
		return m, nil
	case "left":
		if m.menu.formAt == 3 {
			m.menu.keyRevealed = false
		}
		return m, nil
	case "enter":
		return m.menuEnter()
	case "tab":
		// Tab moves to next field (same as down).
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
			m.menu.keyRevealed = false
		}
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.menu.form) > m.menu.formAt && m.menu.form[m.menu.formAt] != "" {
			r := []rune(m.menu.form[m.menu.formAt])
			m.menu.form[m.menu.formAt] = string(r[:len(r)-1])
		}
		return m, nil
	case "ctrl+v":
		if text, err := clipboard.ReadAll(); err == nil && text != "" {
			m.menu.form[m.menu.formAt] += normalizePastedText(text)
		}
		return m, nil
	}
	// Everything else — letters, digits, symbols, space — is text input.
	if len(msg.Runes) > 0 {
		text := string(msg.Runes)
		if msg.Paste {
			text = normalizePastedText(text)
		}
		m.menu.form[m.menu.formAt] += text
		return m, nil
	}
	return m, nil
}

func (m *Model) clampMenuCursor() {
	max := 0
	switch m.menu.kind {
	case menuModels, menuProviderModels:
		max = len(m.filteredModelRows()) - 1
	case menuProviders:
		max = len(m.providerRows()) - 1
	case menuProviderForm:
		// form uses formAt, not cursor
		max = len(m.menu.form) - 1
		if max < 0 {
			max = 0
		}
		if m.menu.formAt > max {
			m.menu.formAt = max
		}
		return
	case menuProviderPredefined:
		max = len(providers.PredefinedProviders()) - 1
	case menuGoal:
		max = len(m.goalTaskRows()) - 1
	}
	if max < 0 {
		max = 0
	}
	if m.menu.cursor > max {
		m.menu.cursor = max
	}
}

func (m Model) menuEnter() (tea.Model, tea.Cmd) {
	switch m.menu.kind {
	case menuModels, menuProviderModels:
		rows := m.filteredModelRows()
		if len(rows) == 0 {
			return m, nil
		}
		selected := rows[minInt(m.menu.cursor, len(rows)-1)]
		if m.modelSwapFn == nil || m.modelSwapper == nil {
			m.appendLine(m.marker.ModelInfo("selected " + selected.ID + " (model swap not wired)"))
			return m.closeMenu()
		}
		m.mode = modeNormal
		m.input.Focus()
		return m, func() tea.Msg { return modelSwapRequestMsg{ModelID: selected.ID, Provider: selected.Provider} }
	case menuProviders:
		return m, nil
	case menuProviderForm:
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
			return m, nil
		}
		savedName := ""
		if m.providerMgr != nil && len(m.menu.form) >= 4 {
			f := m.menu.form
			if m.menu.editName != "" {
				typ, url, key := f[1], f[2], f[3]
				_ = m.providerMgr.Update(m.menu.editName, &typ, &url, &key, nil)
				savedName = m.menu.editName
			} else if strings.TrimSpace(f[0]) != "" {
				_ = m.providerMgr.Add(f[0], f[1], f[2], f[3], "")
				savedName = f[0]
			}
			m.providerMgr.Reload()
			if savedName != "" && m.caps != nil {
				res := m.providerMgr.ScanProvider(savedName, m.caps)
				if res.Err != nil {
					m.appendLine(m.marker.Error(fmt.Errorf("provider %s scan failed: %w", savedName, res.Err)))
				} else if len(res.Models) == 0 {
					m.appendLine(m.palette.InputHint.Render("provider " + savedName + ": key OK, but /v1/models returned 0 models"))
				} else {
					m.appendLine(m.palette.InputHint.Render(fmt.Sprintf("provider %s: key OK, found %d model(s)", savedName, len(res.Models))))
				}
				m.refreshTranscript()
			}
		}
		m.menu = interactiveMenu{kind: menuProviders}
		return m, nil
	case menuProviderPredefined:
		pres := providers.PredefinedProviders()
		if len(pres) == 0 {
			return m, nil
		}
		p := pres[minInt(m.menu.cursor, len(pres)-1)]
		m.menu = interactiveMenu{
			kind:     menuProviderForm,
			form:     []string{p.Name, p.Type, p.BaseURL, ""},
			formAt:   0,
			editName: "",
		}
		return m, nil
	}
	return m, nil
}

func (m Model) menuSpace() (tea.Model, tea.Cmd) {
	if m.menu.kind == menuGoal && m.goalSvc != nil {
		rows := m.goalTaskRows()
		if len(rows) == 0 {
			return m, nil
		}
		t := rows[minInt(m.menu.cursor, len(rows)-1)]
		newStatus := goal.TaskDone
		if t.Status == goal.TaskDone {
			newStatus = goal.TaskPending
		}
		if err := m.goalSvc.SetTaskStatus(context.Background(), "", t.Seq, newStatus); err != nil {
			m.appendLine(m.marker.Error(err))
		}
	}
	return m, nil
}

func (m Model) renderMenuView() string {
	switch m.menu.kind {
	case menuModels:
		return m.renderModelsMenu("Models", "↑↓ move · type filter · Enter select · → enable · ← disable · ESC back")
	case menuProviderModels:
		return m.renderModelsMenu("Models: "+m.menu.provider, "↑↓ move · type filter · Enter select · → enable · ← disable · ESC back")
	case menuProviders:
		return m.renderProvidersMenu()
	case menuProviderForm:
		return m.renderProviderForm()
	case menuProviderPredefined:
		return m.renderPredefinedMenu()
	case menuGoal:
		return m.renderGoalMenu()
	default:
		return ""
	}
}

func (m Model) renderModelsMenu(title, footer string) string {
	rows := m.filteredModelRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(title) + "\n")
	b.WriteString(m.palette.InputHint.Render("filter: "+m.menu.filter) + "\n\n")
	b.WriteString("on provider        name                         context   input       output      caps\n")
	b.WriteString("── ────────────── ──────────────────────────── ───────── ─────────── ─────────── ────\n")
	for i, row := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		// Show enabled/disabled indicator.
		on := "✓"
		if m.menu.kind == menuProviderModels && m.providerMgr != nil && m.providerMgr.IsHidden(row.ID) {
			on = "✗"
		}
		line := fmt.Sprintf("%-2s %-14s %-28s %-9s %-11s %-11s %s", on, row.Provider, row.ID, ctxLen(row.ContextLength), price(row.InputCost), price(row.OutputCost), caps(row))
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	if len(rows) == 0 {
		if m.caps != nil && len(m.caps.All()) == 0 {
			b.WriteString("  scanning providers for models...\n")
		} else {
			b.WriteString("  no matching models\n")
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(footer))
	return b.String()
}

func (m Model) renderProvidersMenu() string {
	rows := m.providerRows()
	active := m.activeProviderName()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Providers") + "\n\n")
	for i, p := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		check := " "
		if p.Name == active {
			check = "✓"
		}
		status := "○ disconnected"
		if p.Connected {
			status = "● connected"
		}
		b.WriteString(fmt.Sprintf("%s[%s] %-14s %-8s %-16s %s\n", prefix, check, p.Name, p.Type, status, p.BaseURL))
	}
	if len(rows) == 0 {
		b.WriteString("  no providers configured\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render("[A]dd [E]dit [D]elete [R]efresh [M]odels [ESC]back"))
	return b.String()
}

// activeProviderName returns the name of the provider that owns
// the currently loaded model, or empty string if unknown.
func (m Model) activeProviderName() string {
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
	labels := []string{"name", "type", "base URL", "API key"}
	var b strings.Builder
	title := "Add provider"
	if m.menu.editName != "" {
		title = "Edit provider: " + m.menu.editName
	}
	b.WriteString(m.palette.PanelTitle.Render(title) + "\n\n")
	for i, label := range labels {
		prefix := "  "
		if i == m.menu.formAt {
			prefix = "❯ "
		}
		value := ""
		if i < len(m.menu.form) {
			value = m.menu.form[i]
		}
		if label == "API key" && value != "" {
			if m.menu.formAt == 3 && m.menu.keyRevealed {
				// On API key field + right arrow pressed → show real key
				value = m.palette.HeaderMode.Render(value)
			} else {
				value = strings.Repeat("*", len([]rune(value)))
			}
		}
		b.WriteString(fmt.Sprintf("%s%-9s %s\n", prefix, label+":", value))
	}
	hint := "type/paste · Ctrl+V paste · Enter next/save · ↑↓ fields · ESC back"
	if m.menu.formAt == 3 {
		if m.menu.keyRevealed {
			hint = "← hide key · type/paste · Ctrl+V paste · Enter save · ESC back"
		} else {
			hint = "→ reveal key · type/paste · Ctrl+V paste · Enter save · ESC back"
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(hint))
	return b.String()
}

func (m Model) renderPredefinedMenu() string {
	pres := providers.PredefinedProviders()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Add provider — pick a template") + "\n\n")
	for i, p := range pres {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		line := fmt.Sprintf("%-14s %-42s %s", p.Name, p.Desc, m.palette.Dim.Render(p.BaseURL))
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	if len(pres) == 0 {
		b.WriteString("  no predefined providers\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render("↑↓ select · Enter pick · ESC back"))
	return b.String()
}

func (m Model) renderGoalMenu() string {
	rows := m.goalTaskRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Goal tasks") + "\n\n")
	for i, t := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		mark := "[ ]"
		if t.Status == goal.TaskDone {
			mark = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s%s %d. %s\n", prefix, mark, t.Seq, t.Title))
	}
	if len(rows) == 0 {
		b.WriteString("  no active goal tasks\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render("Space toggle · [A]dd task · [D]elete · [ESC]back"))
	return b.String()
}

func (m Model) filteredModelRows() []llm.ModelInfo {
	rows := []llm.ModelInfo{}
	if m.modelLister != nil {
		rows = m.modelLister.ListModels()
	}
	if len(rows) == 0 && m.caps != nil {
		rows = m.caps.All()
	}

	// Only show models from configured providers —
	// hide seed/hardcoded models (e.g. gpt-4o-mini)
	// unless their provider is in the [[providers]] list.
	if m.providerMgr != nil {
		configured := m.configuredProviderNames()
		if len(configured) > 0 {
			filtered := rows[:0]
			for _, r := range rows {
				// Once providers are configured, do not display embedded
				// seed models as if they were available from that API key.
				// The scanner must confirm models via /v1/models first.
				if r.Source == llm.SourceSeed {
					continue
				}
				for _, name := range configured {
					if r.Provider == name {
						filtered = append(filtered, r)
						break
					}
				}
			}
			rows = filtered
		}
	}

	if m.menu.provider != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Provider == m.menu.provider {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	// In the global /models view, hide disabled models.
	// In the per-provider view (menuProviderModels), show
	// all models so the user can toggle them with arrows.
	if m.menu.kind == menuModels && m.providerMgr != nil {
		filtered := rows[:0]
		for _, r := range rows {
			if !m.providerMgr.IsHidden(r.ID) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	q := strings.ToLower(m.menu.filter)
	if q != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if fuzzy(strings.ToLower(r.ID+" "+r.Provider), q) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Provider == rows[j].Provider {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Provider < rows[j].Provider
	})
	return rows
}

func (m Model) providerRows() []providers.ProviderInfo {
	if m.providerMgr == nil || m.caps == nil {
		return nil
	}
	return m.providerMgr.List(m.caps)
}

// configuredProviderNames returns the names of all providers
// in the [[providers]] list. Used to filter seed models
// so only models from user-configured providers appear.
func (m Model) configuredProviderNames() []string {
	if m.providerMgr == nil {
		return nil
	}
	return m.providerMgr.Names()
}

func (m Model) goalTaskRows() []goal.Task {
	if m.goalSvc == nil {
		return nil
	}
	rows, _ := m.goalSvc.ListTasks(context.Background(), "")
	return rows
}

func fuzzy(haystack, needle string) bool {
	i := 0
	for _, r := range haystack {
		if i < len(needle) && byte(r) == needle[i] {
			i++
		}
	}
	return i == len(needle)
}
func caps(m llm.ModelInfo) string {
	parts := []string{}
	if m.Vision {
		parts = append(parts, "vision")
	}
	if m.Reasoning {
		parts = append(parts, "reasoning")
	}
	if m.ToolUse {
		parts = append(parts, "tools")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
func price(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", v)
}
func ctxLen(n int) string {
	if n <= 0 {
		return "-"
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
