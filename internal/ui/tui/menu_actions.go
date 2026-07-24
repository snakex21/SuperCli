package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
)

func (m Model) menuEnter() (tea.Model, tea.Cmd) {
	switch m.menu.kind {
	case menuActions:
		return m.selectAction()
	case menuSessions:
		return m.selectSession()
	case menuTranscript:
		return m.selectTranscriptMatch()
	case menuQueue:
		return m.runQueuedTask()
	case menuData:
		return m.runDataAction()
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
	case menuQueue:
		return m.renderQueueMenu()
	case menuData:
		return m.renderDataMenu()
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
