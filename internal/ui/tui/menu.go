package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type menuKind int

const (
	menuNone menuKind = iota
	menuActions
	menuSessions
	menuModels       // enabled models: fast picker used by /model
	menuModelCatalog // every model: visibility manager used by /models
	menuProviders
	menuProviderModels
	menuProviderForm
	menuProviderPredefined
	menuOpenAIAuth
	menuAccounts
	menuAccountLabel
	menuProjects
	menuGoal
	menuReasoning
	menuSettings
	menuCheckpoint
	menuTranscript
)

// CheckpointPreview is intentionally presentation-sized metadata. It contains
// no file contents and is cheap to obtain before a destructive-looking action.
type CheckpointPreview struct {
	ID     string
	Prompt string
	Files  []string
	Redo   bool
}

type interactiveMenu struct {
	kind        menuKind
	cursor      int
	filter      string
	provider    string
	form        []string
	formAt      int
	formErr     string
	editName    string
	keyRevealed bool // true = API key shown in plain text
	sessions    []session.Session

	// /settings panel state. settingsCfg holds the last loaded/saved
	// global config so the panel renders live values; editing/editBuf
	// drive inline integer editing of a numeric knob.
	settingsCfg *config.TomlConfig
	editing     bool
	editBuf     string
	checkpoint  *CheckpointPreview
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

// openModelCatalogMenu opens the complete catalog, including models hidden
// from the fast /model picker. Visibility changes are local and persisted;
// opening the catalog never calls an LLM.
func (m Model) openModelCatalogMenu() (tea.Model, tea.Cmd) {
	if m.providerMgr != nil && m.caps != nil && len(m.caps.All()) == 0 {
		m.providerMgr.ScanModels(m.caps)
	}
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuModelCatalog}
	m.input.Blur()
	return m, nil
}

// providerStatus is the cached result of one async connectivity
// probe for the /providers menu.
type providerStatus struct {
	checked   bool // false = probe still running
	online    bool
	err       string
	latency   time.Duration
	checkedAt time.Time
}

// providerStatusMsg delivers one provider's async probe result.
type providerStatusMsg struct {
	name      string
	online    bool
	err       string
	latency   time.Duration
	checkedAt time.Time
}

// providerSavedMsg delivers the async result of saving a provider
// from the form (scan + test request).
type providerSavedMsg struct {
	name        string
	body        string
	err         error
	form        []string
	editName    string
	wasNew      bool
	rolledBack  bool
	rollbackErr error
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
		if p.Disabled {
			m.providerStatuses[p.Name] = providerStatus{checked: true, err: "disabled (saved, not contacted)"}
			continue
		}
		// Echo and ChatGPT-OAuth (codex) providers have no
		// pingable /v1/models endpoint; mark them online.
		if p.Type == "echo" || p.Type == "codex" {
			m.providerStatuses[p.Name] = providerStatus{checked: true, online: true}
			continue
		}
		m.providerStatuses[p.Name] = providerStatus{} // checking...
		cmds = append(cmds, func() tea.Msg {
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := providers.Ping(ctx, p)
			latency := time.Since(started)
			if err != nil {
				return providerStatusMsg{name: p.Name, online: false, err: err.Error(), latency: latency, checkedAt: time.Now()}
			}
			return providerStatusMsg{name: p.Name, online: true, latency: latency, checkedAt: time.Now()}
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

func (m Model) openReasoningMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuReasoning, cursor: reasoningOptionIndex(llm.ReasoningEffort())}
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
	if m.menu.kind == menuActions {
		return m.handleActionsKey(msg)
	}
	if m.menu.kind == menuSessions {
		return m.handleSessionsKey(msg)
	}
	if m.menu.kind == menuTranscript {
		return m.handleTranscriptKey(msg)
	}
	if m.menu.kind == menuCheckpoint {
		switch msg.String() {
		case "esc", "n":
			return m.closeMenu()
		case "enter", "y":
			name := "undo"
			if m.menu.checkpoint != nil && m.menu.checkpoint.Redo {
				name = "redo"
			}
			return m.dispatchVisualCommand(name, "")
		}
		return m, nil
	}
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

	// /settings integer editing: digits/enter/esc/backspace build the
	// value instead of navigating the list.
	if m.menu.kind == menuSettings && m.menu.editing {
		return m.settingsEditKey(msg)
	}

	key := msg.String()
	lowerKey := strings.ToLower(key)
	if (m.menu.kind == menuModelCatalog || m.menu.kind == menuProviderModels) && m.providerMgr != nil && (key == "A" || key == "X") {
		rows := m.filteredModelRows()
		refs := make([]providers.ModelRef, 0, len(rows))
		for _, row := range rows {
			refs = append(refs, providers.ModelRef{Provider: row.Provider, ID: row.ID})
		}
		m.providerMgr.SetModelRefsHidden(refs, key == "X")
		return m, nil
	}
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
		if isModelVisibilityMenu(m.menu.kind) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				row := rows[minInt(m.menu.cursor, len(rows)-1)]
				m.providerMgr.ShowModelFor(row.Provider, row.ID)
			}
		}
		return m, nil
	case "left":
		// Left arrow: disable/hide model.
		if isModelVisibilityMenu(m.menu.kind) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				row := rows[minInt(m.menu.cursor, len(rows)-1)]
				m.providerMgr.HideModelFor(row.Provider, row.ID)
			}
		}
		return m, nil
	case "a":
		if m.menu.kind == menuProviders {
			m.menu = interactiveMenu{kind: menuProviderPredefined}
			m.input.Blur()
			return m, nil
		}
		// Projects menu: 'a' adds the current directory (same as
		// the trailing "+ add current directory" row). Dispatched
		// through /projects add so the same path runs as the
		// slash command — one source of truth.
		if m.menu.kind == menuProjects {
			if mm, cmd, handled := m.projectsMenuKey(key); handled {
				return mm, cmd
			}
		}
		if m.menu.kind == menuGoal && m.goalSvc != nil {
			_, err := m.goalSvc.AddTask(context.Background(), "", "new task")
			if err != nil {
				m.appendLine(m.marker.Error(err))
			}
		}
		// In model menus ordinary lowercase letters belong to the filter.
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "e":
		if m.menu.kind == menuProviders {
			rows := m.providerRows()
			if len(rows) > 0 {
				p := rows[minInt(m.menu.cursor, len(rows)-1)]
				apiKey := ""
				if m.providerMgr != nil {
					for _, configured := range m.providerMgr.Configured() {
						if configured.Name == p.Name {
							apiKey = configured.APIKey
							break
						}
					}
				}
				// Keep the stored key in the edit form. Rendering masks it by
				// default and the user can reveal it explicitly with Right Arrow.
				// An empty fourth field used to be submitted as an explicit clear,
				// silently deleting a working key on unrelated edits.
				m.menu = interactiveMenu{kind: menuProviderForm, editName: p.Name, form: []string{p.Name, p.Type, p.BaseURL, apiKey, p.Model}}
				m.input.Blur()
			}
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "d":
		// Accounts menu: 'd' logs out the selected account.
		if m.menu.kind == menuAccounts {
			if mm, cmd, handled := m.menuAccountsKey(key); handled {
				return mm, cmd
			}
		}
		// Projects menu: 'd' unregisters the selected project.
		if m.menu.kind == menuProjects {
			if mm, cmd, handled := m.projectsMenuKey(key); handled {
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
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "r":
		if m.menu.kind == menuSettings {
			return m.settingsResetCurrent()
		}
		if m.menu.kind == menuModels && key == "R" {
			return m.openReasoningMenu()
		}
		if key == "R" && (m.menu.kind == menuModelCatalog || m.menu.kind == menuProviderModels) && m.providerMgr != nil && m.caps != nil {
			mgr, caps, provider := m.providerMgr, m.caps, m.menu.provider
			return m, func() tea.Msg {
				if provider == "" {
					mgr.ScanModels(caps)
				} else {
					mgr.ScanProvider(provider, caps)
				}
				return providerScanDoneMsg{}
			}
		}
		if m.menu.kind == menuProviders && m.providerMgr != nil {
			m.providerMgr.Reload()
			if m.caps == nil {
				return m, m.probeProvidersCmd()
			}
			mgr, caps := m.providerMgr, m.caps
			scan := func() tea.Msg {
				mgr.ScanModels(caps)
				return providerScanDoneMsg{}
			}
			return m, tea.Batch(m.probeProvidersCmd(), scan)
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "c":
		// Shortcut to the ChatGPT accounts screen — only on an
		// OpenAI/ChatGPT row (contextual, like [M]/[E]).
		if m.menu.kind == menuProviders && m.cursorOnOpenAIRow() {
			m.menu = interactiveMenu{kind: menuAccounts}
			return m, nil
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "m":
		if m.menu.kind == menuProviders {
			return m.openProviderModelsAtCursor()
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	}
	if len(msg.Runes) > 0 && isModelMenu(m.menu.kind) {
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

func (m Model) openProviderModelsAtCursor() (tea.Model, tea.Cmd) {
	rows := m.providerRows()
	if len(rows) == 0 {
		return m, nil
	}
	p := rows[minInt(m.menu.cursor, len(rows)-1)]
	if p.Disabled {
		m.statusOverride = "provider " + p.Name + " is paused; press Space to enable it"
		return m, statusClearCmd()
	}
	m.menu = interactiveMenu{kind: menuProviderModels, provider: p.Name}
	if mgr, caps := m.providerMgr, m.caps; mgr != nil && caps != nil {
		name := p.Name
		return m, func() tea.Msg {
			mgr.ScanProvider(name, caps)
			return providerScanDoneMsg{}
		}
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
			m.menu.formErr = ""
		}
		return m, nil
	case "ctrl+v":
		if text, err := clipboard.ReadAll(); err == nil && text != "" {
			m.menu.form[m.menu.formAt] += normalizePastedLine(text)
			m.menu.formErr = ""
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
		m.menu.formErr = ""
		return m, nil
	}
	return m, nil
}

func (m *Model) clampMenuCursor() {
	max := 0
	switch m.menu.kind {
	case menuActions:
		max = len(m.filteredActionRows()) - 1
	case menuSessions:
		max = len(m.filteredSessionRows()) - 1
	case menuTranscript:
		max = len(m.filteredTranscriptRows()) - 1
	case menuModels, menuModelCatalog, menuProviderModels:
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
	case menuProjects:
		max = len(m.projectRows()) - 1
	case menuGoal:
		max = len(m.goalTaskRows()) - 1
	case menuReasoning:
		max = len(reasoningMenuOptions()) - 1
	case menuSettings:
		max = len(m.localizedSettingsRows()) - 1
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
	case menuActions:
		return m.selectAction()
	case menuSessions:
		return m.selectSession()
	case menuTranscript:
		return m.selectTranscriptMatch()
	case menuModels:
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
	case menuModelCatalog, menuProviderModels:
		rows := m.filteredModelRows()
		if len(rows) == 0 || m.providerMgr == nil {
			return m, nil
		}
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		if m.providerMgr.IsHiddenFor(row.Provider, row.ID) {
			m.providerMgr.ShowModelFor(row.Provider, row.ID)
		} else {
			m.providerMgr.HideModelFor(row.Provider, row.ID)
		}
		return m, nil
	case menuProviders:
		return m.openProviderModelsAtCursor()
	case menuProviderForm:
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
			return m, nil
		}
		m.menu.formErr = ""
		formSnapshot := append([]string(nil), m.menu.form...)
		editName := m.menu.editName
		wasNew := editName == ""
		savedName := ""
		if m.providerMgr != nil && len(m.menu.form) >= 4 {
			f := m.menu.form
			model := ""
			if len(f) > 4 {
				model = f[4]
			}
			var saveErr error
			if m.menu.editName != "" {
				typ, url, key := f[1], f[2], f[3]
				saveErr = m.providerMgr.Update(m.menu.editName, &typ, &url, &key, &model)
				if saveErr == nil {
					savedName = m.menu.editName
				}
			} else if strings.TrimSpace(f[0]) != "" {
				saveErr = m.providerMgr.Add(f[0], f[1], f[2], f[3], model)
				if saveErr == nil {
					savedName = f[0]
				}
			}
			if saveErr != nil {
				m.menu.formErr = compactProviderError(saveErr)
				return m, nil
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
			baseMsg := providerSavedMsg{
				name:     savedName,
				form:     formSnapshot,
				editName: editName,
				wasNew:   wasNew,
			}
			failed := func(err error) providerSavedMsg {
				msg := baseMsg
				msg.err = err
				if wasNew {
					if rollbackErr := mgr.Remove(savedName); rollbackErr != nil {
						msg.rollbackErr = rollbackErr
						// Remove updates memory before persisting. Restore the
						// on-disk state if that persistence step itself failed.
						mgr.Reload()
					} else {
						msg.rolledBack = true
					}
				}
				return msg
			}

			// Probe into a temporary registry. If /models succeeds but the
			// inference request rejects the credentials, the live picker must
			// not retain ghost models from the rejected provider.
			probeCaps := llm.NewCapabilityRegistry()
			res := mgr.ScanProvider(savedName, probeCaps)
			if res.Err != nil {
				return failed(res.Err)
			}
			if len(res.Models) == 0 {
				baseMsg.body = "endpoint reachable, but it returned 0 models — load/pull a model first"
				return baseMsg
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
				baseMsg.body = fmt.Sprintf("found %d model(s)", len(res.Models))
				return baseMsg
			}
			model := conf.Model
			if model == "" {
				model = res.Models[0]
			}
			if err := providers.VerifyConnectionForProvider(context.Background(), conf.Type, conf.BaseURL, conf.APIKey, model); err != nil {
				return failed(err)
			}
			caps.RegisterAll(probeCaps.All())
			baseMsg.body = fmt.Sprintf("✓ connected — %d model(s), test request OK (%s)", len(res.Models), model)
			return baseMsg
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
			form:     []string{p.Name, p.Type, p.BaseURL, "", ""},
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
			form:   []string{"openai", "openai", "https://api.openai.com/v1", "", ""},
			formAt: 3,
		}
		return m, nil
	case menuAccounts:
		return m.accountsMenuEnter()
	case menuProjects:
		return m.projectsMenuEnter()
	case menuReasoning:
		return m.selectReasoningEffort()
	case menuSettings:
		return m.settingsEnter()
	}
	return m, nil
}

func (m Model) menuSpace() (tea.Model, tea.Cmd) {
	if m.menu.kind == menuProviders && m.providerMgr != nil {
		rows := m.providerRows()
		if len(rows) == 0 {
			return m, nil
		}
		p := rows[minInt(m.menu.cursor, len(rows)-1)]
		if err := m.providerMgr.SetDisabled(p.Name, !p.Disabled); err != nil {
			m.statusOverride = "provider: " + err.Error()
			return m, statusClearCmd()
		}
		m.providerMgr.Reload()
		if p.Disabled && m.caps != nil {
			mgr, caps, name := m.providerMgr, m.caps, p.Name
			scan := func() tea.Msg {
				mgr.ScanProvider(name, caps)
				return providerScanDoneMsg{}
			}
			return m, tea.Batch(m.probeProvidersCmd(), scan)
		}
		return m, m.probeProvidersCmd()
	}
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
	case menuActions:
		return m.renderActionsMenu()
	case menuSessions:
		return m.renderSessionsMenu()
	case menuTranscript:
		return m.renderTranscriptMenu()
	case menuModels:
		return m.renderModelsMenu(m.tr("Enabled models", "Włączone modele"), m.tr("↑↓ select · type to filter · Enter use · R reasoning · Esc back", "↑↓ wybierz · pisz aby filtrować · Enter użyj · R myślenie · Esc wróć"))
	case menuModelCatalog:
		return m.renderModelsMenu(m.tr("Model catalog", "Katalog modeli"), m.tr("↑↓ select · type filter · Enter toggle · A enable visible · X disable visible · R refresh · Esc back", "↑↓ wybierz · pisz filtr · Enter przełącz · A włącz widoczne · X wyłącz widoczne · R odśwież · Esc wróć"))
	case menuProviderModels:
		return m.renderModelsMenu(m.tr("Models · ", "Modele · ")+m.menu.provider, m.tr("↑↓ select · Enter toggle · A enable visible · X disable visible · R refresh · Esc back", "↑↓ wybierz · Enter przełącz · A włącz widoczne · X wyłącz widoczne · R odśwież · Esc wróć"))
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
	case menuProjects:
		return m.renderProjectsMenu()
	case menuGoal:
		return m.renderGoalMenu()
	case menuReasoning:
		return m.renderReasoningMenu()
	case menuSettings:
		return m.renderSettingsMenu()
	case menuCheckpoint:
		return m.renderCheckpointMenu()
	default:
		return ""
	}
}

func (m Model) openCheckpointMenu(redo bool) (tea.Model, tea.Cmd) {
	if m.checkpointPreview == nil {
		return m.dispatchVisualCommand(map[bool]string{false: "undo", true: "redo"}[redo], "")
	}
	preview, err := m.checkpointPreview(redo)
	if err != nil {
		m.statusOverride = err.Error()
		return m.closeMenu()
	}
	preview.Redo = redo
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuCheckpoint, checkpoint: &preview}
	m.input.Blur()
	return m, nil
}

func (m Model) renderCheckpointMenu() string {
	preview := m.menu.checkpoint
	if preview == nil {
		return m.palette.Error.Render("Brak danych checkpointu")
	}
	width := maxInt(24, m.menuWidth())
	action := "Cofnij ostatnią turę"
	verb := "przywrócone do stanu sprzed tury"
	if preview.Redo {
		action = "Ponów cofniętą turę"
		verb = "przywrócone do stanu po turze"
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(action) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateText("Checkpoint "+preview.ID+" · pliki zostaną "+verb+". Rozmowa nie jest usuwana.", width)) + "\n\n")
	if strings.TrimSpace(preview.Prompt) != "" {
		b.WriteString(m.palette.StatusKey.Render("Tura: ") + m.palette.StatusValue.Render(truncateText(preview.Prompt, width-8)) + "\n\n")
	}
	b.WriteString(m.palette.StatusKey.Render(fmt.Sprintf("Pliki (%d):", len(preview.Files))) + "\n")
	limit := minInt(len(preview.Files), maxInt(3, m.height-9))
	for _, file := range preview.Files[:limit] {
		b.WriteString(m.palette.StatusValue.Render("  • "+truncateText(file, width-4)) + "\n")
	}
	if len(preview.Files) > limit {
		b.WriteString(m.palette.Dim.Render(fmt.Sprintf("  … i %d więcej", len(preview.Files)-limit)) + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible("Enter potwierdź · Esc anuluj", width)))
	return b.String()
}

func isModelMenu(kind menuKind) bool {
	return kind == menuModels || kind == menuModelCatalog || kind == menuProviderModels
}

func isModelVisibilityMenu(kind menuKind) bool {
	return kind == menuModelCatalog || kind == menuProviderModels
}

func (m Model) renderModelsMenu(title, footer string) string {
	rows := m.filteredModelRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(fmt.Sprintf("%s · %d", title, len(rows))) + "\n")
	filter := m.menu.filter
	if filter == "" {
		filter = m.tr("start typing", "zacznij pisać")
	}
	b.WriteString(m.palette.InputHint.Render(m.tr("Search: ", "Szukaj: ")+filter) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		available := (m.height - 5) / 2
		start, end = menuWindow(len(rows), m.menu.cursor, available)
	}
	for i := start; i < end; i++ {
		row := rows[i]
		row = m.enrichModelRow(row)
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		state := ""
		if m.menu.kind == menuModels {
			if row.ID == m.reasoningModelName() {
				state = m.tr("[active]", "[aktywny]")
			}
		} else {
			state = "[on]"
			if m.providerMgr != nil && m.providerMgr.IsHiddenFor(row.Provider, row.ID) {
				state = "[off]"
			}
		}
		nameWidth := width - lipgloss.Width(prefix)
		if state != "" {
			nameWidth -= lipgloss.Width(state) + 1
		}
		if nameWidth < 18 {
			nameWidth = 18
		}
		line := prefix + truncateText(row.ID, nameWidth)
		if state != "" {
			line += " " + state
		}
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Bold.Render(line)
		}
		b.WriteString(line + "\n")

		meta := row.Provider + " · ctx " + ctxLen(row.ContextLength) +
			" · in " + m.modelPrice(row, true) + " · out " + m.modelPrice(row, false)
		if c := caps(row); c != "" {
			meta += " · " + c
		}
		if providerState := m.modelProviderState(row.Provider); providerState != "" {
			meta += " · " + providerState
		}
		b.WriteString(m.palette.Dim.Render(truncateText("    "+meta, width)) + "\n")
	}
	if len(rows) == 0 {
		if m.caps != nil && len(m.caps.All()) == 0 {
			b.WriteString("  " + m.tr("scanning providers for models...", "skanowanie modeli dostawców...") + "\n")
		} else {
			b.WriteString("  " + m.tr("no matching models", "brak pasujących modeli") + "\n")
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(footer, width)))
	return b.String()
}

func (m Model) modelProviderState(provider string) string {
	if m.providerMgr != nil && m.providerMgr.IsDisabled(provider) {
		return m.tr("provider paused", "dostawca wstrzymany")
	}
	if status, ok := m.providerStatuses[provider]; ok && status.checked && !status.online {
		return "offline"
	}
	return ""
}

func (m Model) enrichModelRow(row llm.ModelInfo) llm.ModelInfo {
	subscription := m.isSubscriptionProviderName(row.Provider)
	if m.caps != nil {
		if extra, priceSafe, ok := m.lookupModelMetadata(row); ok {
			if row.ContextLength == 0 {
				row.ContextLength = extra.ContextLength
			}
			if priceSafe && !subscription && row.InputCost == 0 {
				row.InputCost = extra.InputCost
			}
			if priceSafe && !subscription && row.OutputCost == 0 {
				row.OutputCost = extra.OutputCost
			}
		}
	}
	if !subscription && (row.InputCost == 0 || row.OutputCost == 0) {
		if rate, key := credits.RateForProvider(row.Provider, row.ID); key != "default" {
			if row.InputCost == 0 {
				row.InputCost = rate.InputPer1k * 1000
			}
			if row.OutputCost == 0 {
				row.OutputCost = rate.OutputPer1k * 1000
			}
		}
	}
	return row
}

func (m Model) lookupModelMetadata(row llm.ModelInfo) (info llm.ModelInfo, priceSafe bool, ok bool) {
	if m.caps == nil || row.ID == "" {
		return llm.ModelInfo{}, false, false
	}
	if row.Provider != "" {
		if extra, ok := m.caps.Get(row.Provider + "/" + row.ID); ok {
			return extra, true, true
		}
	}

	// OpenRouter uses provider-prefixed ids (deepseek/deepseek-v4-flash),
	// while a direct provider often exposes only the short id
	// (deepseek-v4-flash). Use a unique suffix match as metadata only, so
	// non-OpenRouter rows can still display OpenRouter's context_length.
	shortID := modelIDSuffix(row.ID)
	var match llm.ModelInfo
	found := false
	for _, extra := range m.caps.All() {
		if strings.EqualFold(extra.ID, row.ID) || !strings.Contains(extra.ID, "/") {
			continue
		}
		if !strings.EqualFold(modelIDSuffix(extra.ID), shortID) {
			continue
		}
		if found {
			return llm.ModelInfo{}, false, false
		}
		match = extra
		found = true
	}
	return match, false, found
}

func modelIDSuffix(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

func (m Model) modelPrice(row llm.ModelInfo, input bool) string {
	if m.isSubscriptionProviderName(row.Provider) {
		return "sub"
	}
	if input {
		return price(row.InputCost)
	}
	return price(row.OutputCost)
}

func (m Model) isSubscriptionProviderName(name string) bool {
	if strings.EqualFold(name, config.ProviderCodex) {
		return true
	}
	if m.providerMgr == nil || name == "" {
		return false
	}
	for _, p := range m.providerMgr.Configured() {
		if p.Name == name && p.Type == config.ProviderCodex {
			return true
		}
	}
	return false
}

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

func (m Model) renderGoalMenu() string {
	rows := m.goalTaskRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Goal tasks", "Zadania celu")) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		start, end = menuWindow(len(rows), m.menu.cursor, m.height-5)
	}
	for i := start; i < end; i++ {
		t := rows[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		mark := "[ ]"
		if t.Status == goal.TaskDone {
			mark = "[x]"
		}
		b.WriteString(truncateText(fmt.Sprintf("%s%s %d. %s", prefix, mark, t.Seq, t.Title), width) + "\n")
	}
	if len(rows) == 0 {
		b.WriteString("  " + m.tr("no active goal tasks", "brak aktywnych zadań celu") + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("Space toggle · A add task · D delete · Esc back", "Space przełącz · A dodaj zadanie · D usuń · Esc wróć"), width)))
	return b.String()
}

type reasoningMenuOption struct {
	Label string
	Value string
	Desc  string
}

func reasoningMenuOptions() []reasoningMenuOption {
	return []reasoningMenuOption{
		{Label: "off / provider default", Value: "", Desc: "do not send a reasoning/thinking budget"},
		{Label: "none", Value: "none", Desc: "explicitly disable when the provider supports a none value"},
		{Label: "minimal", Value: "minimal", Desc: "smallest thinking budget if accepted by the backend"},
		{Label: "low", Value: "low", Desc: "low thinking budget"},
		{Label: "medium", Value: "medium", Desc: "balanced thinking budget"},
		{Label: "high", Value: "high", Desc: "larger thinking budget"},
		{Label: "xhigh", Value: "xhigh", Desc: "maximum thinking budget where supported"},
	}
}

func (m Model) localizedReasoningMenuOptions() []reasoningMenuOption {
	if m.language != "pl" {
		return reasoningMenuOptions()
	}
	return []reasoningMenuOption{
		{Label: "wyłączone / domyślne dostawcy", Value: "", Desc: "nie wysyłaj budżetu myślenia"},
		{Label: "brak", Value: "none", Desc: "wyłącz jawnie, jeśli dostawca obsługuje tę wartość"},
		{Label: "minimalne", Value: "minimal", Desc: "najmniejszy budżet myślenia akceptowany przez backend"},
		{Label: "niskie", Value: "low", Desc: "niski budżet myślenia"},
		{Label: "średnie", Value: "medium", Desc: "zrównoważony budżet myślenia"},
		{Label: "wysokie", Value: "high", Desc: "większy budżet myślenia"},
		{Label: "maksymalne", Value: "xhigh", Desc: "największy obsługiwany budżet myślenia"},
	}
}

func reasoningOptionIndex(value string) int {
	for i, opt := range reasoningMenuOptions() {
		if opt.Value == value {
			return i
		}
	}
	return 0
}

func (m Model) reasoningModelName() string {
	if m.modelSwapper != nil && m.modelSwapper.CurrentModel() != "" {
		return m.modelSwapper.CurrentModel()
	}
	if m.llm != nil {
		return m.llm.Name()
	}
	return "no-model"
}

func (m Model) selectReasoningEffort() (tea.Model, tea.Cmd) {
	opts := reasoningMenuOptions()
	if len(opts) == 0 {
		return m.closeMenu()
	}
	opt := opts[minInt(m.menu.cursor, len(opts)-1)]
	if err := llm.SetReasoningEffort(opt.Value); err != nil {
		m.statusOverride = fmt.Sprintf("reasoning: %v", err)
	} else {
		if opt.Value == "" {
			m.statusOverride = "reasoning: off (provider default)"
		} else {
			model := m.reasoningModelName()
			_, effective, adjusted := llm.ReasoningEffortAdjustment(model)
			if adjusted {
				m.statusOverride = fmt.Sprintf("reasoning: %s (effective %s for %s)", opt.Value, effective, model)
			} else {
				m.statusOverride = "reasoning: " + opt.Value
			}
		}
		m.persistReasoningEffort(opt.Value)
	}
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusOverrideClearMsg{} })
}

func (m Model) renderReasoningMenu() string {
	width := m.menuWidth()
	model := m.reasoningModelName()
	configured, effective, adjusted := llm.ReasoningEffortAdjustment(model)
	if configured == "" {
		configured = m.tr("off / provider default", "wyłączone / domyślne dostawcy")
	}
	if effective == "" {
		effective = m.tr("not sent", "niewysyłane")
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Reasoning effort", "Poziom myślenia")) + "\n\n")
	b.WriteString(truncateText(fmt.Sprintf("model:      %s", model), width) + "\n")
	b.WriteString(truncateVisible(fmt.Sprintf(m.tr("configured: %s", "ustawione:   %s"), configured), width) + "\n")
	if adjusted {
		b.WriteString(truncateVisible(fmt.Sprintf(m.tr("effective:  %s (adjusted from backend evidence)", "efektywne:   %s (dopasowane na podstawie backendu)"), effective), width) + "\n")
	} else {
		b.WriteString(truncateVisible(fmt.Sprintf(m.tr("effective:  %s", "efektywne:   %s"), effective), width) + "\n")
	}
	if supported, ok := llm.SupportedReasoningEfforts(model); ok {
		b.WriteString(truncateVisible("backend:    "+strings.Join(supported, " | "), width) + "\n")
	} else if llm.SupportsReasoningEffort(model) {
		b.WriteString(truncateVisible(m.tr("backend:    unknown yet — will learn from API errors", "backend:    jeszcze nieznany — zostanie rozpoznany z błędów API"), width) + "\n")
	} else {
		b.WriteString(truncateVisible(m.tr("backend:    model family does not advertise reasoning effort", "backend:    rodzina modelu nie zgłasza obsługi poziomu myślenia"), width) + "\n")
	}
	b.WriteString("\n")
	opts := m.localizedReasoningMenuOptions()
	supported, learned := llm.SupportedReasoningEfforts(model)
	for i, opt := range opts {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		label := opt.Label
		if opt.Value != "" && learned && !containsString(supported, opt.Value) {
			label += m.tr(" (not in learned backend list)", " (brak na wykrytej liście backendu)")
		}
		plain := truncateText(fmt.Sprintf("%-34s %s", label, opt.Desc), width-2)
		line := plain
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(prefix + line)
		} else {
			line = prefix + m.palette.Dim.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("↑↓ move · Enter apply · Esc back", "↑↓ wybierz · Enter zastosuj · Esc wróć"), width)))
	return b.String()
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (m Model) filteredModelRows() []llm.ModelInfo {
	rows := []llm.ModelInfo{}
	if m.modelLister != nil {
		rows = m.modelLister.ListModels()
	}
	if len(rows) == 0 && m.caps != nil {
		rows = m.caps.All()
	}
	// Merge the provider's persisted last-known inventory. This keeps local
	// and remote self-hosted models available after a restart even when their
	// server is currently offline. Runtime registry rows win on duplicates.
	if m.providerMgr != nil {
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Provider+"\x00"+row.ID] = struct{}{}
		}
		for _, p := range m.providerMgr.ListConfigured(m.caps) {
			for _, row := range p.Models {
				key := row.Provider + "\x00" + row.ID
				if _, ok := seen[key]; ok {
					continue
				}
				rows = append(rows, row)
				seen[key] = struct{}{}
			}
		}
	}

	// Only show models from configured providers —
	// hide seed/hardcoded models (e.g. gpt-4o-mini)
	// unless their provider is in the [[providers]] list.
	if m.providerMgr != nil {
		catalog := m.menu.kind != menuModels
		visible := func(provider, id string) bool {
			if catalog {
				return m.providerMgr.ModelCatalogVisible(provider, id)
			}
			return m.providerMgr.ModelVisible(provider, id)
		}
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
				if !visible(r.Provider, r.ID) {
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
					if r.Source != llm.SourceSeed && visible(r.Provider, r.ID) {
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
			if m.providerMgr != nil && !m.providerMgr.ModelCatalogVisible(r.Provider, r.ID) {
				continue
			}
			if r.Provider == m.menu.provider {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	// The fast /model picker hides disabled models. The complete /models
	// catalog and per-provider view keep them visible so the user can turn
	// them back on without editing config.toml.
	if m.menu.kind == menuModels && m.providerMgr != nil {
		filtered := rows[:0]
		for _, r := range rows {
			if !m.providerMgr.ModelVisible(r.Provider, r.ID) {
				continue
			}
			if !m.providerMgr.IsHiddenFor(r.Provider, r.ID) {
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
