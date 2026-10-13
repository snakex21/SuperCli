package tui

import (
	"context"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

func (m onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if inputs, ok := msg.(terminalInputBatchMsg); ok {
		return updateTerminalInputBatch(m, inputs)
	}
	if keys, ok := msg.(terminalKeyBatchMsg); ok {
		return updateTerminalKeyBatch(m, keys)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && (m.step == onboardMenu || m.step == onboardModels) {
			if msg.Button == tea.MouseButtonWheelUp {
				return m.Update(tea.KeyMsg{Type: tea.KeyUp})
			}
			if msg.Button == tea.MouseButtonWheelDown {
				return m.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
		}
		return m, nil
	case onboardDetectedMsg:
		m.detected = msg.servers
		m.choices = buildChoices(msg.servers, m.language)
		m.step = onboardMenu
		return m, nil
	case onboardModelsMsg:
		if msg.request == nil || m.request != msg.request || m.step != onboardLoadModels {
			return m, nil
		}
		m.cancelRequest()
		if msg.typ != "" {
			m.result.Type = msg.typ
		}
		if msg.err != nil || len(msg.models) == 0 {
			if msg.err != nil {
				m.errMsg = msg.err.Error()
			} else {
				m.errMsg = m.tr("tui.onboard_update.cbcb87d11b")
			}
			m.step = onboardMenu
			m.cursor = 0
			return m, nil
		}
		m.models = msg.models
		m.cursor = 0
		m.step = onboardModels
		return m, nil
	case onboardVerifyMsg:
		if msg.request == nil || m.request != msg.request || m.step != onboardVerify {
			return m, nil
		}
		m.cancelRequest()
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.step = onboardMenu
			m.cursor = 0
			return m, nil
		}
		m.step = onboardDone
		return m, tea.Quit
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyCtrlC:
		m.cancelRequest()
		m.aborted = true
		return m, tea.Quit
	case tea.KeyEsc:
		m.cancelRequest()
		if m.step == onboardMenu || m.step == onboardDetect {
			m.aborted = true
			return m, tea.Quit
		}
		m.step = onboardMenu
		m.cursor = 0
		m.input = ""
		return m, nil
	}

	switch m.step {
	case onboardMenu:
		n := len(m.filteredChoices())
		switch key.String() {
		case "up", "shift+tab":
			m.cursor = maxInt(0, m.cursor-1)
		case "down", "tab":
			m.cursor = minInt(maxInt(0, n-1), m.cursor+1)
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = maxInt(0, n-1)
		case "pgup":
			m.cursor = maxInt(0, m.cursor-8)
		case "pgdown":
			m.cursor = minInt(maxInt(0, n-1), m.cursor+8)
		case "backspace", "ctrl+h":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
			m.cursor = 0
		case "ctrl+u":
			m.filter, m.cursor = "", 0
		case "ctrl+v":
			if text, err := onboardClipboard(); err == nil {
				m.filter += normalizePastedLine(text)
			}
			m.cursor = 0
		case "enter":
			return m.choose()
		default:
			for _, r := range key.Runes {
				if r >= ' ' && r != 0x7f {
					m.filter += string(r)
				}
			}
			m.cursor = 0
		}
	case onboardAuthMethod:
		switch key.String() {
		case "up", "shift+tab", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "tab", "j":
			if m.cursor < 1 {
				m.cursor++
			}
		case "1", "2":
			m.cursor = int(key.String()[0] - '1')
			return m.chooseAuth()
		case "enter":
			return m.chooseAuth()
		}
	case onboardModels:
		switch key.String() {
		case "up", "shift+tab", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "tab", "j":
			if m.cursor < len(m.models)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.models) > 0 {
				m.result.Model = m.models[minInt(m.cursor, len(m.models)-1)]
			}
			return m.startVerify()
		}
	case onboardURL:
		switch key.Type {
		case tea.KeyEnter:
			url := strings.TrimSpace(m.input)
			if url == "" {
				return m, nil
			}
			m.result.BaseURL = strings.TrimRight(url, "/")
			m.step = onboardKey
			m.input = ""
		default:
			m.input = editOnboardText(m.input, key)
		}
	case onboardKey:
		switch key.Type {
		case tea.KeyEnter:
			m.result.APIKey = strings.TrimSpace(m.input)
			m.input = ""
			return m.startLoadModels()
		default:
			m.input = editOnboardText(m.input, key)
		}
	}
	return m, nil
}

// onboardClipboard is replaceable in tests.
var onboardClipboard = clipboard.ReadAll

// editOnboardText applies one key to a single-line setup field (URL, API
// key): typing, bracketed paste, Ctrl+V, Backspace/Ctrl+H and Ctrl+U.
func editOnboardText(s string, key tea.KeyMsg) string {
	if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
		text := string(key.Runes)
		if key.Paste {
			text = normalizePastedLine(text)
		}
		return s + text
	}
	switch key.String() {
	case "backspace", "ctrl+h":
		if r := []rune(s); len(r) > 0 {
			return string(r[:len(r)-1])
		}
	case "ctrl+u":
		return ""
	case "ctrl+v":
		if text, err := onboardClipboard(); err == nil {
			return s + normalizePastedLine(text)
		}
	}
	return s
}

// choose applies the main menu selection.
func (m onboardModel) choose() (tea.Model, tea.Cmd) {
	rows := m.filteredChoices()
	if len(rows) == 0 {
		return m, nil
	}
	c := rows[minInt(m.cursor, len(rows)-1)]
	m.errMsg = ""
	switch c.kind {
	case "local":
		m.result = OnboardResult{Name: c.local.Name, Type: c.local.Type, BaseURL: c.local.BaseURL}
		if len(c.local.Models) == 0 {
			// No models installed: save the provider anyway; the
			// user can pull a model and pick it with /models later.
			m.step = onboardDone
			return m, tea.Quit
		}
		m.models = c.local.Models
		m.cursor = 0
		m.step = onboardModels
		return m, nil
	case "openai":
		m.result = OnboardResult{Name: c.provider.Name, Type: c.provider.Type, BaseURL: c.provider.BaseURL}
		m.cursor = 0
		m.step = onboardAuthMethod
		return m, nil
	case "custom":
		m.result = OnboardResult{Name: "custom", Type: "auto"}
		m.step = onboardURL
		m.input = ""
		return m, nil
	case "local-manual":
		m.result = OnboardResult{Name: c.provider.Name, Type: c.provider.Type, BaseURL: c.provider.BaseURL}
		return m.startLoadModels()
	case "template":
		m.result = OnboardResult{Name: c.provider.Name, Type: c.provider.Type, BaseURL: c.provider.BaseURL}
		m.input = ""
		m.step = onboardKey
		return m, nil
	case "echo":
		m.result = OnboardResult{Name: "echo", Type: "echo"}
		m.step = onboardDone
		return m, tea.Quit
	}
	return m, nil
}

// chooseAuth applies the OpenAI auth-method selection.
func (m onboardModel) chooseAuth() (tea.Model, tea.Cmd) {
	if m.cursor == 0 {
		// Sign in with ChatGPT — main.go runs the OAuth browser
		// flow after the wizard exits.
		m.result.AuthMethod = AuthChatGPT
		m.result.Type = "codex"
		m.result.Model = "gpt-5.5"
		m.step = onboardDone
		return m, tea.Quit
	}
	m.result.AuthMethod = AuthAPIKey
	m.step = onboardKey
	m.input = ""
	return m, nil
}

// startLoadModels only contacts the provider explicitly selected by the user.
func (m onboardModel) startLoadModels() (tea.Model, tea.Cmd) {
	m.cancelRequest()
	provider := config.ProviderConf{Name: m.result.Name, Type: m.result.Type, BaseURL: m.result.BaseURL, APIKey: m.result.APIKey}
	ctx, cancel := context.WithTimeout(context.Background(), llm.ProviderDiscoveryTimeout)
	request := &onboardRequest{cancel: cancel}
	m.request, m.step = request, onboardLoadModels
	return m, func() tea.Msg {
		defer cancel()
		if provider.Type == "auto" {
			typ, err := llm.DetectProviderProtocol(ctx, provider.BaseURL, provider.APIKey)
			if err != nil {
				typ = "openai"
			}
			provider.Type = typ
		}
		models, err := providers.DiscoverModelIDs(ctx, provider)
		return onboardModelsMsg{request: request, models: models, typ: provider.Type, err: err}
	}
}

// startVerify runs the existing single connection test, and Esc cancels it.
func (m onboardModel) startVerify() (tea.Model, tea.Cmd) {
	m.cancelRequest()
	result := m.result
	ctx, cancel := context.WithCancel(context.Background())
	request := &onboardRequest{cancel: cancel}
	m.request, m.step = request, onboardVerify
	return m, func() tea.Msg {
		defer cancel()
		key := llm.KiloDefaultKey(result.BaseURL, result.APIKey)
		return onboardVerifyMsg{request: request, err: providers.VerifyConnectionForProvider(ctx, result.Type, result.BaseURL, key, result.Model, m.dataDir)}
	}
}
