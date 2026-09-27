package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

func (m onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
				m.errMsg = m.tr("the server returned no models — load/pull a model first", "serwer nie zwrócił modeli — najpierw załaduj model")
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
		case "up":
			m.cursor = maxInt(0, m.cursor-1)
		case "down":
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
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
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
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
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
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				r := []rune(m.input)
				m.input = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.input += string(key.Runes)
		}
	case onboardKey:
		switch key.Type {
		case tea.KeyEnter:
			m.result.APIKey = strings.TrimSpace(m.input)
			m.input = ""
			return m.startLoadModels()
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				r := []rune(m.input)
				m.input = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.input += string(key.Runes)
		}
	}
	return m, nil
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
