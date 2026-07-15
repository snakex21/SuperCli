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
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

// Auth methods for the OpenAI provider.
const (
	AuthAPIKey  = "api-key"
	AuthChatGPT = "chatgpt"
)

// OnboardResult is what the wizard hands back to main.go.
type OnboardResult struct {
	// Skipped is true when the user aborted (Ctrl+C / Esc on
	// the menu) — nothing should be written.
	Skipped bool
	// Name is the [[providers]] entry name ("ollama",
	// "lmstudio", "openai", "custom", "echo").
	Name string
	// Type is the provider type ("openai", "anthropic", "codex" or "echo").
	Type string
	// AuthMethod is set for the OpenAI provider: AuthAPIKey or
	// AuthChatGPT. AuthChatGPT means main.go must run the OAuth
	// login flow (the wizard cannot open a browser itself).
	AuthMethod string
	BaseURL    string
	APIKey     string
	// Model is the model the user picked (may be empty when the
	// server reported none).
	Model string
}

const (
	lmStudioDefaultURL = "http://localhost:1234/v1"
	ollamaDefaultURL   = "http://localhost:11434/v1"
	openaiDefaultURL   = "https://api.openai.com/v1"
)

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

// onboardChoice is one selectable row in the main menu.
type onboardChoice struct {
	label string
	desc  string
	local *providers.LocalServer // non-nil for detected servers
	kind  string                 // "local", "openai", "custom", "lmstudio-manual", "ollama-manual", "echo"
}

type onboardDetectedMsg struct{ servers []providers.LocalServer }
type onboardModelsMsg struct {
	models []string
	err    error
}
type onboardVerifyMsg struct{ err error }

type onboardModel struct {
	step    onboardStep
	cursor  int
	input   string
	result  OnboardResult
	aborted bool

	detected []providers.LocalServer
	choices  []onboardChoice
	models   []string
	errMsg   string // last verification/load error shown above the menu
	language string
}

func (m onboardModel) tr(english, polish string) string { return textFor(m.language, english, polish) }

func (m onboardModel) Init() tea.Cmd {
	return func() tea.Msg {
		return onboardDetectedMsg{servers: providers.DetectLocalServers(context.Background())}
	}
}

// buildChoices assembles the menu: detected local servers first,
// then the static options.
func buildChoices(detected []providers.LocalServer, languages ...string) []onboardChoice {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	tr := func(en, pl string) string { return textFor(language, en, pl) }
	var out []onboardChoice
	haveOllama, haveLMStudio := false, false
	for i := range detected {
		s := detected[i]
		switch s.Name {
		case "ollama":
			haveOllama = true
		case "lmstudio":
			haveLMStudio = true
		}
		desc := fmt.Sprintf(tr("detected · %d model(s) · %s", "wykryto · %d modeli · %s"), len(s.Models), s.BaseURL)
		out = append(out, onboardChoice{label: s.Label, desc: desc, local: &detected[i], kind: "local"})
	}
	if !haveOllama {
		out = append(out, onboardChoice{label: "Ollama", desc: tr("not detected · ", "nie wykryto · ") + ollamaDefaultURL, kind: "ollama-manual"})
	}
	if !haveLMStudio {
		out = append(out, onboardChoice{label: "LM Studio", desc: tr("not detected · ", "nie wykryto · ") + lmStudioDefaultURL, kind: "lmstudio-manual"})
	}
	out = append(out,
		onboardChoice{label: "OpenAI", desc: tr("ChatGPT account or API key", "konto ChatGPT lub klucz API"), kind: "openai"},
		onboardChoice{label: tr("OpenAI-compatible API", "API zgodne z OpenAI"), desc: tr("any endpoint · URL + key", "dowolny endpoint · URL + klucz"), kind: "custom"},
		onboardChoice{label: "Offline / echo", desc: tr("no LLM, just try the UI", "bez LLM, tylko test interfejsu"), kind: "echo"},
	)
	return out
}

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

func (m onboardModel) View() string {
	p := DefaultPalette()
	var b strings.Builder
	b.WriteString(p.PanelTitle.Render("✻ SuperCli") + p.PanelMuted.Render(m.tr(" — first-run setup", " — pierwsze uruchomienie")) + "\n")
	switch m.step {
	case onboardDetect:
		b.WriteString(p.PanelMuted.Render(m.tr("Looking for local LLM servers (Ollama, LM Studio)...", "Szukam lokalnych serwerów LLM (Ollama, LM Studio)...")) + "\n")
	case onboardMenu:
		b.WriteString(p.PanelMuted.Render(m.tr("No provider is configured yet. Pick one (saved to config.toml in the data dir):", "Nie skonfigurowano jeszcze dostawcy. Wybierz jednego (zapis w config.toml):")) + "\n")
		if m.errMsg != "" {
			b.WriteString(p.Error.Render("✗ "+m.errMsg) + "\n")
		}
		b.WriteString("\n")
		for i, c := range m.choices {
			line := fmt.Sprintf("%d. %-22s %s", i+1, c.label, c.desc)
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render("> "+line))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render("  "+line))
			}
		}
		b.WriteString("\n" + p.InputHint.Render(m.tr("↑↓ + Enter (or number) · Esc to skip", "↑↓ + Enter (lub numer) · Esc pomiń")) + "\n")
	case onboardAuthMethod:
		b.WriteString("\n" + m.tr("How do you want to use OpenAI?", "Jak chcesz korzystać z OpenAI?") + "\n\n")
		opts := []string{
			m.tr("Sign in with your ChatGPT account (uses your subscription limits)", "Zaloguj konto ChatGPT (używa limitów subskrypcji)"),
			m.tr("API key (pay-as-you-go platform.openai.com key)", "Klucz API (rozliczenie za użycie z platform.openai.com)"),
		}
		for i, o := range opts {
			line := fmt.Sprintf("%d. %s", i+1, o)
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render("> "+line))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render("  "+line))
			}
		}
		b.WriteString("\n" + p.InputHint.Render(m.tr("↑↓ + Enter · Esc back", "↑↓ + Enter · Esc wróć")) + "\n")
	case onboardURL:
		b.WriteString("\n" + m.tr("Base URL of the OpenAI-compatible server:", "Bazowy URL serwera zgodnego z OpenAI:") + "\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render(">"), m.input)
		b.WriteString("\n" + p.InputHint.Render(m.tr("Enter to confirm · Esc back", "Enter potwierdź · Esc wróć")) + "\n")
	case onboardKey:
		masked := strings.Repeat("*", len([]rune(m.input)))
		b.WriteString("\n" + m.tr("API key (Enter to skip if the server needs none):", "Klucz API (Enter pomija, jeśli serwer go nie wymaga):") + "\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render(">"), masked)
		b.WriteString("\n" + p.InputHint.Render(m.tr("Enter to confirm · Esc back", "Enter potwierdź · Esc wróć")) + "\n")
	case onboardLoadModels:
		b.WriteString(p.PanelMuted.Render("\n"+m.tr("Fetching the model list from ", "Pobieram listę modeli z ")+m.result.BaseURL+"...") + "\n")
	case onboardModels:
		b.WriteString(p.PanelMuted.Render(m.tr("Pick a model (", "Wybierz model (")+m.result.Name+"):") + "\n\n")
		// Show a window of up to 10 models around the cursor.
		start := 0
		if m.cursor > 9 {
			start = m.cursor - 9
		}
		end := minInt(start+10, len(m.models))
		for i := start; i < end; i++ {
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render("> "+m.models[i]))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render("  "+m.models[i]))
			}
		}
		if end < len(m.models) {
			fmt.Fprintf(&b, "%s\n", p.Dim.Render(fmt.Sprintf(m.tr("  ... %d more", "  ... i jeszcze %d"), len(m.models)-end)))
		}
		b.WriteString("\n" + p.InputHint.Render(m.tr("↑↓ + Enter · Esc back", "↑↓ + Enter · Esc wróć")) + "\n")
	case onboardVerify:
		b.WriteString(p.PanelMuted.Render("\n"+m.tr("Testing the connection (asking the model to say OK)...", "Testuję połączenie (proszę model o odpowiedź OK)...")) + "\n")
	case onboardDone:
		b.WriteString(p.Success.Render(m.tr("✓ connected — saved. Starting chat...", "✓ połączono — zapisano. Uruchamiam czat...")) + "\n")
	}
	return b.String()
}

// RunOnboarding runs the wizard in its own bubbletea program
// and returns the user's choice. A TTY error or abort returns
// Skipped=true so the caller falls back to echo mode.
func RunOnboarding(language string) OnboardResult {
	p := tea.NewProgram(onboardModel{language: normalizeLanguage(language)})
	final, err := p.Run()
	if err != nil {
		return OnboardResult{Skipped: true}
	}
	m, ok := final.(onboardModel)
	if !ok || m.aborted || m.step != onboardDone {
		return OnboardResult{Skipped: true}
	}
	return m.result
}
