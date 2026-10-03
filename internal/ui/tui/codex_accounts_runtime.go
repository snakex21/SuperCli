package tui

import "supercli/internal/llm"

// Auth changes rebuild the transport once at the next foreground boundary.
// The active run keeps its provider; ordinary prompts never rebuild the pool.
func (m *Model) refreshCodexAccounts() error {
	if !m.codexAccountsChanged {
		return nil
	}
	if m.modelSwapFn == nil || m.modelSwapper == nil {
		m.codexAccountsChanged = false
		return nil
	}
	var model string
	switch p := llm.Unwrap(m.llm).(type) {
	case *llm.CodexProvider:
		model = p.Name()
	case *llm.RouterProvider:
		if m.providerMgr == nil {
			m.codexAccountsChanged = false
			return nil
		}
		codex := false
		for _, p := range m.providerMgr.Configured() {
			if p.Name == m.activeProvider && p.Type == "codex" {
				codex = true
				break
			}
		}
		if !codex {
			m.codexAccountsChanged = false
			return nil
		}
		model = p.ModelName()
	default:
		m.codexAccountsChanged = false
		return nil
	}
	replacement, err := m.modelSwapFn(model, m.activeProvider)
	if err != nil {
		return err
	}
	if setter, ok := m.modelSwapper.(interface{ SetAccountProvider(llm.Provider) error }); ok {
		if err := setter.SetAccountProvider(replacement); err != nil {
			return err
		}
	} else {
		m.modelSwapper.SetModel(replacement)
	}
	m.llm = replacement
	m.codexAccountsChanged = false
	m.refreshRuntimeHUD()
	return nil
}
