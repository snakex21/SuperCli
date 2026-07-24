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

	tea "github.com/charmbracelet/bubbletea"

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
