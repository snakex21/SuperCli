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
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
