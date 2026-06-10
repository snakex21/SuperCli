package tui

// First-run onboarding wizard (wave 4). When SuperCli starts
// with no provider configured at all, this minimal flow asks
// which provider to use and (for an OpenAI-compatible server)
// the URL + API key, then main.go writes config.toml and drops
// the user straight into chat. Deliberately tiny: three
// choices, two text fields, no profile questions.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// OnboardResult is what the wizard hands back to main.go.
type OnboardResult struct {
	// Skipped is true when the user aborted (Ctrl+C / Esc on
	// the menu) — nothing should be written.
	Skipped bool
	// Name is the [[providers]] entry name ("lmstudio",
	// "custom", "echo").
	Name string
	// Type is the provider type ("openai" or "echo").
	Type string
	BaseURL string
	APIKey  string
}

const lmStudioDefaultURL = "http://localhost:1234/v1"

type onboardStep int

const (
	onboardMenu onboardStep = iota
	onboardURL
	onboardKey
	onboardDone
)

type onboardModel struct {
	step    onboardStep
	cursor  int
	input   string
	result  OnboardResult
	aborted bool
}

var onboardChoices = []string{
	"LM Studio / local server (" + lmStudioDefaultURL + ")",
	"OpenAI-compatible API (enter URL + key)",
	"Offline / echo (no LLM, try the UI)",
}

func (m onboardModel) Init() tea.Cmd { return nil }

func (m onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyCtrlC:
		m.aborted = true
		return m, tea.Quit
	case tea.KeyEsc:
		if m.step == onboardMenu {
			m.aborted = true
			return m, tea.Quit
		}
		m.step = onboardMenu
		m.input = ""
		return m, nil
	}

	switch m.step {
	case onboardMenu:
		switch key.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(onboardChoices)-1 {
				m.cursor++
			}
		case "1", "2", "3":
			m.cursor = int(key.String()[0] - '1')
			return m.choose()
		case "enter":
			return m.choose()
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
			m.step = onboardDone
			return m, tea.Quit
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

// choose applies the menu selection.
func (m onboardModel) choose() (tea.Model, tea.Cmd) {
	switch m.cursor {
	case 0:
		m.result = OnboardResult{Name: "lmstudio", Type: "openai", BaseURL: lmStudioDefaultURL}
		m.step = onboardDone
		return m, tea.Quit
	case 1:
		m.result = OnboardResult{Name: "custom", Type: "openai"}
		m.step = onboardURL
		m.input = ""
		return m, nil
	default:
		m.result = OnboardResult{Name: "echo", Type: "echo"}
		m.step = onboardDone
		return m, tea.Quit
	}
}

func (m onboardModel) View() string {
	p := DefaultPalette()
	var b strings.Builder
	b.WriteString(p.PanelTitle.Render("✻ SuperCli") + p.PanelMuted.Render(" — first-run setup") + "\n")
	b.WriteString(p.PanelMuted.Render("No provider is configured yet. Pick one (saved to .supercli/config.toml):") + "\n\n")
	switch m.step {
	case onboardMenu:
		for i, c := range onboardChoices {
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render(fmt.Sprintf("❯ %d. %s", i+1, c)))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render(fmt.Sprintf("  %d. %s", i+1, c)))
			}
		}
		b.WriteString("\n" + p.InputHint.Render("up/down + Enter (or 1-3) · Esc to skip") + "\n")
	case onboardURL:
		b.WriteString("Base URL of the OpenAI-compatible server:\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render("❯"), m.input)
		b.WriteString("\n" + p.InputHint.Render("Enter to confirm · Esc to go back") + "\n")
	case onboardKey:
		masked := strings.Repeat("*", len([]rune(m.input)))
		b.WriteString("API key (Enter to skip if the server needs none):\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render("❯"), masked)
		b.WriteString("\n" + p.InputHint.Render("Enter to confirm · Esc to go back") + "\n")
	case onboardDone:
		b.WriteString(p.Success.Render("Saved. Starting chat...") + "\n")
	}
	return b.String()
}

// RunOnboarding runs the wizard in its own bubbletea program
// and returns the user's choice. A TTY error or abort returns
// Skipped=true so the caller falls back to echo mode.
func RunOnboarding() OnboardResult {
	p := tea.NewProgram(onboardModel{})
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
