package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
)

type menuKind int

const (
	menuNone menuKind = iota
	menuModels
	menuProviders
	menuProviderModels
	menuProviderForm
	menuProviderPredefined
	menuOpenAIAuth
	menuAccounts
	menuAccountLabel
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

// providerStatus is the cached result of one async connectivity
// probe for the /providers menu.
type providerStatus struct {
	checked bool // false = probe still running
	online  bool
	err     string
}

// providerStatusMsg delivers one provider's async probe result.
type providerStatusMsg struct {
	name   string
	online bool
	err    string
}

// providerSavedMsg delivers the async result of saving a provider
// from the form (scan + test request).
type providerSavedMsg struct {
	name string
	body string
	err  error
}

// providerScanDoneMsg signals that a background model scan
// finished; the menu re-renders with the registry contents.
type providerScanDoneMsg struct{}

func (m Model) openProvidersMenu() (tea.Model, tea.Cmd) {
	if m.providerMgr != nil {
		m.providerMgr.Reload()
	}
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviders}
	m.input.Blur()
	// Render instantly; probe connectivity in the background.
	return m, m.probeProvidersCmd()
}

// probeProvidersCmd resets the status cache to "checking" and
// returns a batch of tea.Cmds, one ping per configured provider.
// Bubbletea principle: View never blocks; slow IO runs in Cmds
// and the results pop in as they arrive.
func (m *Model) probeProvidersCmd() tea.Cmd {
	if m.providerMgr == nil {
		return nil
	}
	confs := m.providerMgr.Configured()
	m.providerStatuses = make(map[string]providerStatus, len(confs))
	cmds := make([]tea.Cmd, 0, len(confs))
	for _, p := range confs {
		p := p
		// Echo and ChatGPT-OAuth (codex) providers have no
		// pingable /v1/models endpoint; mark them online.
		if p.Type == "echo" || p.Type == "codex" {
			m.providerStatuses[p.Name] = providerStatus{checked: true, online: true}
			continue
		}
		m.providerStatuses[p.Name] = providerStatus{} // checking...
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := providers.Ping(ctx, p)
			if err != nil {
				return providerStatusMsg{name: p.Name, online: false, err: err.Error()}
			}
			return providerStatusMsg{name: p.Name, online: true}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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

	// Account label prompt: typing/enter/backspace go to its own
	// handler so characters build the label instead of navigating.
	if m.menu.kind == menuAccountLabel {
		if mm, cmd, handled := m.accountLabelKey(msg.String()); handled {
			return mm, cmd
		}
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
		// Accounts menu: 'd' logs out the selected account.
		if m.menu.kind == menuAccounts {
			if mm, cmd, handled := m.menuAccountsKey(key); handled {
				return mm, cmd
			}
		}
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
			return m, m.probeProvidersCmd()
		}
		return m, nil
	case "c":
		// Shortcut to the ChatGPT accounts screen — only on an
		// OpenAI/ChatGPT row (contextual, like [M]/[E]).
		if m.menu.kind == menuProviders && m.cursorOnOpenAIRow() {
			m.menu = interactiveMenu{kind: menuAccounts}
			return m, nil
		}
		return m, nil
	case "m":
		if m.menu.kind == menuProviders {
			rows := m.providerRows()
			if len(rows) > 0 {
				idx := minInt(m.menu.cursor, len(rows)-1)
				name := rows[idx].Name
				m.menu = interactiveMenu{kind: menuProviderModels, provider: name}
				// Scan this provider's models in the background so
				// the menu opens instantly.
				if mgr, caps := m.providerMgr, m.caps; mgr != nil && caps != nil {
					return m, func() tea.Msg {
						mgr.ScanProvider(name, caps)
						return providerScanDoneMsg{}
					}
				}
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
			m.menu.form[m.menu.formAt] += normalizePastedLine(text)
		}
		return m, nil
	}
	// Everything else — letters, digits, symbols, space — is text input.
	if len(msg.Runes) > 0 {
		text := string(msg.Runes)
		if msg.Paste {
			text = normalizePastedLine(text)
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
	case menuOpenAIAuth:
		max = 1
	case menuAccounts:
		max = len(m.accountRows()) - 1
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
		// Apply the swap RIGHT NOW (synchronously) on confirm:
		// rebuild the provider, persist the choice to config, and
		// kick the Codex usage refresh — all before this returns.
		// Closing the CLI immediately after picking therefore keeps
		// the model, and the HUD limit refreshes without a send.
		// (Previously this only emitted an async modelSwapRequestMsg.)
		m.applyModelSwap(selected.ID, selected.Provider)
		m.refreshTranscript()
		return m.closeMenu()
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
		}
		m.menu = interactiveMenu{kind: menuProviders}
		if savedName == "" || m.caps == nil {
			return m, m.probeProvidersCmd()
		}
		// Scan the provider's models and run a tiny test request
		// ("Say OK") in the background, then report the outcome.
		mgr, caps := m.providerMgr, m.caps
		verifyCmd := func() tea.Msg {
			res := mgr.ScanProvider(savedName, caps)
			if res.Err != nil {
				return providerSavedMsg{name: savedName, err: res.Err}
			}
			if len(res.Models) == 0 {
				return providerSavedMsg{name: savedName, body: "endpoint reachable, but it returned 0 models — load/pull a model first"}
			}
			// Test request against the first model.
			var conf *config.ProviderConf
			for _, p := range mgr.Configured() {
				if p.Name == savedName {
					p := p
					conf = &p
					break
				}
			}
			if conf == nil {
				return providerSavedMsg{name: savedName, body: fmt.Sprintf("found %d model(s)", len(res.Models))}
			}
			model := conf.Model
			if model == "" {
				model = res.Models[0]
			}
			if err := providers.VerifyConnection(context.Background(), conf.BaseURL, conf.APIKey, model); err != nil {
				return providerSavedMsg{name: savedName, err: err}
			}
			return providerSavedMsg{name: savedName, body: fmt.Sprintf("✓ connected — %d model(s), test request OK (%s)", len(res.Models), model)}
		}
		return m, tea.Batch(m.probeProvidersCmd(), verifyCmd)
	case menuProviderPredefined:
		pres := providers.PredefinedProviders()
		if len(pres) == 0 {
			return m, nil
		}
		p := pres[minInt(m.menu.cursor, len(pres)-1)]
		// OpenAI is one provider with two auth methods: ChatGPT
		// account (OAuth) or API key. Ask which one to use.
		if p.Name == "openai" {
			m.menu = interactiveMenu{kind: menuOpenAIAuth}
			return m, nil
		}
		m.menu = interactiveMenu{
			kind:     menuProviderForm,
			form:     []string{p.Name, p.Type, p.BaseURL, ""},
			formAt:   0,
			editName: "",
		}
		return m, nil
	case menuOpenAIAuth:
		if m.menu.cursor == 0 {
			// Sign in with ChatGPT: open the accounts screen, which
			// lists logged-in accounts and lets the user add/remove
			// them (round-robin pool). First-time users see an empty
			// list with a single "add account" action.
			m.menu = interactiveMenu{kind: menuAccounts}
			return m, nil
		}
		// API key: prefill the regular provider form.
		m.menu = interactiveMenu{
			kind:   menuProviderForm,
			form:   []string{"openai", "openai", "https://api.openai.com/v1", ""},
			formAt: 3,
		}
		return m, nil
	case menuAccounts:
		return m.accountsMenuEnter()
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
	case menuOpenAIAuth:
		return m.renderOpenAIAuthMenu()
	case menuAccounts:
		return m.renderAccountsMenu()
	case menuAccountLabel:
		return m.renderAccountLabelMenu()
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
	header := fmt.Sprintf("    %-14s %-8s %-24s %-12s %s", "name", "type", "model", "status", "endpoint")
	b.WriteString(m.palette.InputHint.Render(header) + "\n")
	b.WriteString(m.palette.Dim.Render("    ────────────── ──────── ──────────────────────── ──────────── ────────────") + "\n")
	for i, p := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		check := " "
		if p.Name == active {
			check = "✓"
		}
		model := p.Model
		if model == "" {
			model = "-"
		}
		if len(model) > 24 {
			model = model[:21] + "..."
		}
		name, typ := displayProvider(p.Name, p.Type)
		statusText, statusStyled := m.providerStatusCell(p.Name)
		// Pad with the unstyled width, then swap in the styled text
		// so ANSI codes don't break column alignment.
		line := fmt.Sprintf("%-14s %-8s %-24s %-12s", name, typ, model, statusText)
		line = strings.Replace(line, statusText, statusStyled, 1) + " " + m.palette.Dim.Render(p.BaseURL)
		full := prefix + check + " " + line
		if i == m.menu.cursor {
			full = prefix + m.palette.HeaderMode.Render(check+" "+fmt.Sprintf("%-14s %-8s %-24s %-12s", name, typ, model, statusText)) + " " + m.palette.Dim.Render(p.BaseURL)
		}
		b.WriteString(full + "\n")
		if st, ok := m.providerStatuses[p.Name]; ok && st.checked && !st.online && st.err != "" {
			errLine := st.err
			if len(errLine) > 70 {
				errLine = errLine[:70] + "..."
			}
			b.WriteString(m.palette.Error.Render("        "+errLine) + "\n")
		}
	}
	if len(rows) == 0 {
		b.WriteString("  no providers configured — press A to add one\n")
	}
	hint := "↑↓ move · [A]dd [E]dit [D]elete [R]echeck [M]odels"
	if m.cursorOnOpenAIRow() {
		hint += " [C]hatGPT accounts"
	}
	hint += " · ✓ = active · ESC back"
	b.WriteString("\n" + m.palette.InputHint.Render(hint))
	return b.String()
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
		plain = "⋯ checking"
		styled = m.palette.InputHint.Render(plain)
	case st.online:
		plain = "● online"
		styled = m.palette.Success.Render(plain)
	default:
		plain = "○ offline"
		styled = m.palette.Error.Render(plain)
	}
	return plain, styled
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

func (m Model) renderOpenAIAuthMenu() string {
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("OpenAI — choose how to sign in") + "\n\n")
	opts := []string{
		"Sign in with your ChatGPT account (uses your subscription limits)",
		"API key (pay-as-you-go platform.openai.com key)",
	}
	for i, o := range opts {
		prefix := "  "
		line := o
		if i == m.menu.cursor {
			prefix = "❯ "
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Dim.Render(line)
		}
		b.WriteString(prefix + line + "\n")
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
			filtered := make([]llm.ModelInfo, 0, len(rows))
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
			// A4 guard: if the provider-name filter would leave
			// the picker EMPTY (registry entries lost their
			// Provider field, or the startup scan failed), fall
			// back to all non-seed rows. An imperfect list beats
			// a blank menu that looks like data loss.
			if len(filtered) == 0 {
				for _, r := range rows {
					if r.Source != llm.SourceSeed {
						filtered = append(filtered, r)
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

// providerRows returns the configured providers WITHOUT network
// probes — this runs in the render path on every keypress, so it
// must stay cheap. Live status comes from m.providerStatuses
// (filled by async pings).
func (m Model) providerRows() []providers.ProviderInfo {
	if m.providerMgr == nil {
		return nil
	}
	return m.providerMgr.ListConfigured(m.caps)
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
