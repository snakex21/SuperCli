package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
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
	menuQueue
	menuData
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
	tasks       []session.QueuedTask

	// /settings panel state. settingsCfg holds the last loaded/saved
	// global config so the panel renders live values; editing/editBuf
	// drive inline integer editing of a numeric knob.
	settingsCfg *config.TomlConfig
	editing     bool
	editBuf     string
	editTaskID  string
	moveTaskID  string
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
