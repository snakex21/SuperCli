package tui

// First-run setup shares the provider catalog used by the GUI and TUI.
// Only local servers are probed before selection. Model discovery and the
// existing connection test run for the selected provider, then main.go
// persists the result in the portable data directory.

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm/providers"
)

const (
	AuthAPIKey  = "api-key"
	AuthChatGPT = "chatgpt"
)

type OnboardResult struct {
	Skipped    bool
	Name       string
	Type       string
	AuthMethod string // main.go handles the ChatGPT OAuth browser flow
	BaseURL    string
	APIKey     string
	Model      string
}

type onboardStep int

const (
	onboardDetect onboardStep = iota
	onboardMenu
	onboardAuthMethod
	onboardURL
	onboardKey
	onboardLoadModels
	onboardModels
	onboardVerify
	onboardDone
)

type onboardChoice struct {
	label    string
	desc     string
	local    *providers.LocalServer
	kind     string
	provider providers.PredefinedProvider
}

type onboardRequest struct{ cancel context.CancelFunc }
type onboardDetectedMsg struct{ servers []providers.LocalServer }
type onboardModelsMsg struct {
	request *onboardRequest
	models  []string
	typ     string
	err     error
}
type onboardVerifyMsg struct {
	request *onboardRequest
	err     error
}

type onboardModel struct {
	step          onboardStep
	cursor        int
	input, filter string
	result        OnboardResult
	aborted       bool
	width, height int
	request       *onboardRequest

	detected []providers.LocalServer
	choices  []onboardChoice
	models   []string
	errMsg   string
	language string
	dataDir  string
}

func (m onboardModel) tr(key string) string { return textFor(m.language, key) }

func (m onboardModel) Init() tea.Cmd {
	return func() tea.Msg {
		return onboardDetectedMsg{servers: providers.DetectLocalServers(context.Background())}
	}
}

// Detected local servers stay first. Every other entry comes from the same
// catalog as the normal provider chooser; there is no second endpoint list.
func buildChoices(detected []providers.LocalServer, languages ...string) []onboardChoice {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	presentation := Model{language: language}
	templates := presentation.providerTemplateRows()
	byName := make(map[string]providers.PredefinedProvider, len(templates))
	for _, template := range templates {
		byName[template.Name] = template
	}
	var out []onboardChoice
	seen := make(map[string]bool)
	for i := range detected {
		server := &detected[i]
		if seen[server.Name] {
			continue
		}
		seen[server.Name] = true
		out = append(out, onboardChoice{
			label: presentation.providerTemplateLabel(server.Name),
			desc: fmt.Sprintf(presentation.tr("tui.onboard.d3d7c1d6d0"),
				len(server.Models), server.BaseURL),
			local: server, kind: "local", provider: byName[server.Name],
		})
	}
	for _, template := range templates {
		if seen[template.Name] {
			continue
		}
		choice := onboardChoice{label: presentation.providerTemplateLabel(template.Name),
			desc: template.Desc, kind: "template", provider: template}
		switch template.Name {
		case "openai", "custom":
			choice.kind = template.Name
		case "ollama", "lmstudio":
			choice.kind = "local-manual"
			choice.desc = presentation.tr("tui.onboard.3f9cb35955") + template.BaseURL
		}
		out = append(out, choice)
	}
	return append(out, onboardChoice{label: textFor(language, "tui.other.c4c5af7386"),
		desc: presentation.tr("tui.onboard.1a0eda6f97"), kind: "echo"})
}

func (m onboardModel) filteredChoices() []onboardChoice {
	query := normalizeProviderSearch(m.filter)
	if query == "" {
		return m.choices
	}
	var rows []onboardChoice
	for _, choice := range m.choices {
		search := strings.Join([]string{choice.label, choice.desc, choice.provider.Name, choice.provider.BaseURL}, " ")
		if strings.Contains(normalizeProviderSearch(search), query) {
			rows = append(rows, choice)
		}
	}
	return rows
}

func (m *onboardModel) cancelRequest() {
	if m.request != nil {
		m.request.cancel()
		m.request = nil
	}
}
