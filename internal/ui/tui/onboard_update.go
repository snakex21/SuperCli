package tui

// First-run onboarding wizard. When SuperCli starts with no
// provider configured at all, this flow:
//
//  1. probes Ollama (localhost:11434) and LM Studio
//     (localhost:1234) in parallel with short timeouts,
//  2. shows detected local servers as the FIRST menu options,
//     each expanding into an arrow-key model picker,
//  3. still offers OpenAI (API key or ChatGPT account),
//     any OpenAI-compatible endpoint, and offline echo,
//  4. verifies the chosen provider+model with a tiny test
//     request ("Say OK") and only finishes on success —
//     failures show a human-readable hint and return to the
//     menu.
//
// main.go writes the result to config.toml and drops the user
// straight into chat.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func (m onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case onboardDetectedMsg:
		m.detected = msg.servers
		m.choices = buildChoices(msg.servers, m.language)
		m.step = onboardMenu
		return m, nil
	case onboardModelsMsg:
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
		m.aborted = true
		return m, tea.Quit
	case tea.KeyEsc:
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
		n := len(m.choices)
		switch key.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < n-1 {
				m.cursor++
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(key.String()[0] - '1')
			if idx < n {
				m.cursor = idx
				return m.choose()
			}
		case "enter":
			return m.choose()
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
	if len(m.choices) == 0 {
		return m, nil
	}
	c := m.choices[minInt(m.cursor, len(m.choices)-1)]
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
		m.result = OnboardResult{Name: "openai", Type: "openai", BaseURL: openaiDefaultURL}
		m.cursor = 0
		m.step = onboardAuthMethod
		return m, nil
	case "custom":
		m.result = OnboardResult{Name: "custom", Type: "openai"}
		m.step = onboardURL
		m.input = ""
		return m, nil
	case "ollama-manual":
		m.result = OnboardResult{Name: "ollama", Type: "openai", BaseURL: ollamaDefaultURL}
		return m.startLoadModels()
	case "lmstudio-manual":
		m.result = OnboardResult{Name: "lmstudio", Type: "openai", BaseURL: lmStudioDefaultURL}
		return m.startLoadModels()
	default: // echo
		m.result = OnboardResult{Name: "echo", Type: "echo"}
		m.step = onboardDone
		return m, tea.Quit
	}
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

// startLoadModels lists the server's models in the background.
func (m onboardModel) startLoadModels() (tea.Model, tea.Cmd) {
	baseURL, apiKey := m.result.BaseURL, m.result.APIKey
	m.step = onboardLoadModels
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		models, err := llm.ListProviderModels(ctx, baseURL, apiKey)
		return onboardModelsMsg{models: models, err: err}
	}
}

// startVerify fires the "Say OK" test request in the background.
func (m onboardModel) startVerify() (tea.Model, tea.Cmd) {
	baseURL, apiKey, model := m.result.BaseURL, m.result.APIKey, m.result.Model
	m.step = onboardVerify
	return m, func() tea.Msg {
		return onboardVerifyMsg{err: providers.VerifyConnectionForProvider(context.Background(), m.result.Type, baseURL, apiKey, model)}
	}
}
