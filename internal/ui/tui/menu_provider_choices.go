package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/uilang"
)

func (m Model) providerTemplateRows() []providers.PredefinedProvider {
	rows := append([]providers.PredefinedProvider{{
		Name: "custom", Type: "auto",
		Desc: m.tr("tui.menu_provider_choices.eab17c432f"),
	}}, providers.PredefinedProviders()...)
	for i := range rows {
		if rows[i].Name == "custom" {
			continue
		}
		rows[i].Desc = uilang.ProviderDescription(m.language, rows[i].Name, rows[i].Desc)
	}
	query := normalizeProviderSearch(m.menu.filter)
	if query == "" {
		return rows
	}
	filtered := rows[:0]
	for _, row := range rows {
		text := strings.Join([]string{m.providerTemplateLabel(row.Name), row.Name, row.Desc, row.BaseURL}, " ")
		if strings.Contains(normalizeProviderSearch(text), query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

var providerSearchAccents = strings.NewReplacer(
	"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n",
	"ó", "o", "ś", "s", "ź", "z", "ż", "z",
)

func normalizeProviderSearch(value string) string {
	return providerSearchAccents.Replace(strings.ToLower(strings.TrimSpace(value)))
}

func (m Model) providerTemplateLabel(name string) string {
	switch name {
	case "custom":
		return m.tr("tui.menu_provider_choices.5fb0795eaf")
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "lmstudio":
		return "LM Studio"
	case "ollama":
		return "Ollama"
	case "zen":
		return "OpenCode Zen"
	case "openrouter":
		return "OpenRouter"
	case "cline":
		return "Cline"
	case "nous":
		return "Nous Portal"
	default:
		return name
	}
}

func (m Model) selectedConfiguredProvider() (providers.ProviderInfo, bool) {
	rows := m.providerRows()
	if m.menu.cursor < 0 || m.menu.cursor >= len(rows) {
		return providers.ProviderInfo{}, false
	}
	return rows[m.menu.cursor], true
}

type providerProtocolOption struct{ value, label string }

func (m Model) providerProtocolOptions() []providerProtocolOption {
	return []providerProtocolOption{
		{"auto", m.tr("tui.menu_provider_choices.04a4542a50")},
		{"openai", "OpenAI Chat Completions"},
		{"responses", "OpenAI Responses API"},
		{"anthropic", "Anthropic Messages"},
		{"opencode", "OpenCode gateway"},
	}
}

func (m Model) providerProtocolLabel(value string) string {
	for _, option := range m.providerProtocolOptions() {
		if option.value == value {
			return option.label
		}
	}
	return value
}

func (m *Model) cycleProviderProtocol(delta int) {
	options := m.providerProtocolOptions()
	selected := 0
	for i, option := range options {
		if option.value == m.menu.form[1] {
			selected = i
			break
		}
	}
	m.menu.form[1] = options[(selected+delta+len(options))%len(options)].value
	m.menu.formErr = ""
}

// The request identity prevents a late result from saving an abandoned form.
type providerProtocolRequest struct{ cancel context.CancelFunc }
type providerProtocolDetectedMsg struct {
	request *providerProtocolRequest
	typ     string
	err     error
}

func (m Model) detectProviderProtocol() (tea.Model, tea.Cmd) {
	if m.menu.providerDetection != nil {
		return m, nil
	}
	baseURL, key := strings.TrimSpace(m.menu.form[2]), m.menu.form[3]
	if baseURL == "" {
		m.menu.formAt = 2
		m.menu.formErr = m.tr("tui.menu_provider_choices.53f816d891")
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), llm.ProviderDiscoveryTimeout)
	request := &providerProtocolRequest{cancel: cancel}
	m.menu.providerDetection = request
	return m, func() tea.Msg {
		defer cancel()
		typ, err := llm.DetectProviderProtocol(ctx, baseURL, key)
		return providerProtocolDetectedMsg{request: request, typ: typ, err: err}
	}
}

func (m *Model) cancelProviderDetection() {
	if request := m.menu.providerDetection; request != nil {
		request.cancel()
		m.menu.providerDetection = nil
	}
}

func (m Model) finishProviderDetection(msg providerProtocolDetectedMsg) (tea.Model, tea.Cmd) {
	if msg.request == nil || m.mode != modeMenu || m.menu.kind != menuProviderForm || m.menu.providerDetection != msg.request {
		return m, nil
	}
	m.cancelProviderDetection()
	m.menu.form = append([]string(nil), m.menu.form...)
	m.menu.form[1] = msg.typ
	if msg.err != nil || msg.typ == "" {
		// Match the GUI: an inconclusive passive probe uses OpenAI-compatible
		// chat, and normal provider verification still checks the connection.
		m.menu.form[1] = "openai"
		m.setStatus(m.tr("tui.menu_provider_choices.75a25cd7f9"), false)
	}
	return m.menuEnter()
}
