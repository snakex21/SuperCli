package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent/planmode"
	"supercli/internal/tools/mentions"
	"supercli/internal/tools/shellescape"
)

// startPrompt is the single foreground-run path used by both the composer and
// the persistent task queue. Keeping it centralized guarantees queued work has
// identical cancellation, mentions, plan-mode and persistence semantics.
func (m Model) startPrompt(text string) (tea.Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	if isQuitCommand(text) {
		m.quitting = true
		return m, tea.Quit
	}
	// Bare "q"/"quit"/"exit" (no slash): show the exit tip
	// once, then treat further occurrences as regular input.
	if low := strings.ToLower(text); (low == "q" || low == "quit" || low == "exit") && !m.tipShown {
		m.tipShown = true
		m.appendLine(m.palette.InputHint.Render("tip: use Ctrl+C or /quit to exit; q is regular input"))
		m.refreshTranscript()
		return m, nil
	}
	if cmd := ParseSlashCommand(text); cmd != nil {
		return m.dispatchSlashCommand(*cmd)
	}
	if shellescape.IsShellEscape(text) {
		return m.dispatchShellEscape(text)
	}
	if m.agent == nil {
		m.appendLine(m.marker.NoAgent())
		return m, nil
	}
	if m.onRunStart != nil {
		m.onRunStart()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel.Arm(cancelRun, cancel)
	m.chat.addUser("> " + text)
	m.appendLineToTranscript("> " + text)
	m.busy = true
	m.current = ""
	m.refreshTranscript()
	home, planMode, ag := m.home, m.planMode, m.agent
	return m, func() tea.Msg {
		prompt := text
		remaining, mentionPaths := mentions.Parse(text)
		var mentionCount, mentionTokens int
		if len(mentionPaths) > 0 {
			ments := mentions.Resolve(home, mentionPaths, 0)
			prompt = mentions.FormatBlock(ments, remaining)
			mentionCount = len(mentionPaths)
			mentionTokens = mentions.TotalTokens(ments)
		}
		runPrompt := prompt
		if planMode {
			runPrompt = planmode.WrapPrompt(prompt)
		}
		ch, err := ag.Run(ctx, runPrompt)
		if err != nil {
			cancel()
			return runStartMsg{err: err}
		}
		return runStartMsg{ch: ch, mentionCount: mentionCount, mentionTokens: mentionTokens}
	}
}
