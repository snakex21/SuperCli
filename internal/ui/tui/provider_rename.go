package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm/providers"
)

// providerRenameUsage is the /providers rename syntax. Names containing
// spaces are renamed from the /providers menu (E edit, change the Name).
const providerRenameUsage = "/providers rename <name> <new_name>"

// renameProvider renames a configured provider and every reference to it
// (config layers, saved sessions, context budgets, live model registry), then
// keeps this TUI's own state pointing at the new name. The API key lives in
// the provider entry and moves with it; it is never read here.
func (m *Model) renameProvider(oldName, newName string) error {
	oldName, newName = strings.TrimSpace(oldName), strings.TrimSpace(newName)
	if m.providerMgr == nil {
		return fmt.Errorf("%s", m.tr("tui.slash.77ae580bb6"))
	}
	var sessions providers.SessionProviderRenamer
	if m.sessionStore != nil {
		sessions = m.sessionStore
	}
	var budgets providers.ContextBudgetRenamer
	if m.modelContexts != nil {
		budgets = m.modelContexts
	}
	if err := providers.RenameProviderEverywhere(m.providerMgr, oldName, newName, sessions, budgets, m.caps); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	if m.activeProvider == oldName {
		m.activeProvider = newName
		if setter, ok := m.modelSwapper.(contextProviderSetter); ok {
			setter.SetContextProvider(newName)
		}
	}
	if status, ok := m.providerStatuses[oldName]; ok {
		delete(m.providerStatuses, oldName)
		m.providerStatuses[newName] = status
	}
	return nil
}

// handleSlashProviderRename runs "/providers rename <name> <new_name>"
// synchronously: it is local file/database work, and the TUI state
// (active provider name) must change together with the config.
func (m Model) handleSlashProviderRename(args []string) (tea.Model, tea.Cmd) {
	dm := m.marker
	if len(args) != 2 {
		return m, func() tea.Msg { return slashResultMsg{Body: dm.Diff(providerRenameUsage)} }
	}
	if m.providerMgr != nil {
		m.providerMgr.Reload()
	}
	if err := m.renameProvider(args[0], args[1]); err != nil {
		return m, func() tea.Msg { return slashResultMsg{Err: err} }
	}
	body := fmt.Sprintf(m.tr("tui.provider_rename.done"), args[0], args[1])
	return m, func() tea.Msg { return slashResultMsg{Body: dm.Diff(body)} }
}
