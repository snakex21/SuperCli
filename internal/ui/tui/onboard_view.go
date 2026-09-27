package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m onboardModel) View() string {
	p := DefaultPalette()
	var b strings.Builder
	header := p.PanelTitle.Render("✻ SuperCli") + p.PanelMuted.Render(m.tr(" — first-run setup", " — pierwsze uruchomienie"))
	if m.width > 0 {
		header = truncateVisible(header, m.width)
	}
	b.WriteString(header + "\n")
	switch m.step {
	case onboardDetect:
		b.WriteString(p.PanelMuted.Render(m.tr("Looking for local LLM servers (Ollama, LM Studio)...", "Szukam lokalnych serwerów LLM (Ollama, LM Studio)...")) + "\n")
	case onboardMenu:
		return b.String() + m.renderProviderChoices()
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
		b.WriteString("\n" + m.tr("Provider base URL (connection type will be detected):", "Bazowy URL dostawcy (typ połączenia zostanie wykryty):") + "\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render(">"), m.input)
		b.WriteString("\n" + p.InputHint.Render(m.tr("Enter to confirm · Esc back", "Enter potwierdź · Esc wróć")) + "\n")
	case onboardKey:
		b.WriteString("\n" + p.PanelTitle.Render(m.result.Name) + p.PanelMuted.Render(" · "+m.result.BaseURL) + "\n")
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
func RunOnboarding(language string, dataDirs ...string) OnboardResult {
	initial := onboardModel{language: normalizeLanguage(language)}
	if len(dataDirs) > 0 {
		initial.dataDir = dataDirs[0]
	}
	p := tea.NewProgram(initial, tea.WithMouseCellMotion())
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

func (m onboardModel) renderProviderChoices() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	if width < 24 || height < 8 {
		return truncateVisible(m.tr("Resize terminal · Esc skip", "Powiększ okno · Esc pomiń"), width)
	}
	presentation := Model{
		language: m.language, palette: DefaultPalette(),
		width: minInt(width, 120), height: height - 1,
		menu: interactiveMenu{kind: menuProviderPredefined, cursor: m.cursor, filter: m.filter, formErr: m.errMsg},
	}
	page := menuPage{
		title: m.tr("Choose a provider", "Wybierz dostawcę"),
		subtitle: m.tr("Ready integrations or your own endpoint · saved in the portable data folder",
			"Gotowe integracje lub własny endpoint · zapis w przenośnym folderze danych"),
		searchable: true,
		footer:     m.tr("↑↓ choose · Enter confirm · Esc skip", "↑↓ wybierz · Enter potwierdź · Esc pomiń"),
	}
	for i, row := range m.filteredChoices() {
		page.items = append(page.items, menuListItem{label: row.label, meta: row.desc})
		if i == m.cursor {
			page.detailTitle = row.label
			page.detail = []string{row.desc, "", row.provider.BaseURL}
			if row.provider.Type != "" {
				page.detail = append(page.detail, "", presentation.providerProtocolLabel(row.provider.Type))
			}
		}
	}
	return presentation.renderMenuPage(page)
}
