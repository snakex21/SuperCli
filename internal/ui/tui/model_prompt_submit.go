package tui

import (
	"context"
	"fmt"
	"strings"
	"supercli/internal/ui/attachments"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent/planmode"
	"supercli/internal/llm"
	"supercli/internal/tools/mentions"
	"supercli/internal/tools/shellescape"
)

// startPrompt is the single foreground-run path used by both the composer and
// the persistent task queue. Keeping it centralized guarantees queued work has
// identical cancellation, mentions, plan-mode and persistence semantics.
func (m Model) startPrompt(text string) (tea.Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		if len(m.pendingAttachments) == 0 {
			return m, nil
		}
		text = m.tr("Inspect the attached files.", "Przeanalizuj załączone pliki.")
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
	ctx, cancel := context.WithCancel(llm.WithOpenCodeSession(context.Background(), m.sessionID))
	ctx = llm.WithCallSink(ctx, m.sessionUsageSink())
	m.cancel.Arm(cancelRun, cancel)
	selected := append([]string(nil), m.pendingAttachments...)
	visible := text
	if len(selected) > 0 {
		visible += "\n\n" + attachmentDisplay(selected)
	}
	m.chat.addUser("> " + visible)
	m.appendLineToTranscript("> " + visible)
	m.busy = true
	m.submittingDraft = text
	if m.drafts != nil {
		m.drafts.Update(DraftSnapshot{SessionID: m.sessionID, Text: text, Attachments: selected})
	}
	m.resetCurrent()
	m.refreshTranscript()
	m.viewport.GotoBottom()
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
		if len(selected) > 0 {
			target, ok := ag.(interface {
				SetNextUserAddon(string)
				SetNextUserImages([]llm.ImageRef)
			})
			if !ok {
				cancel()
				return runStartMsg{err: fmt.Errorf("agent does not support attachments"), draft: text}
			}
			staged, err := attachments.StagePicked(home, selected)
			if err != nil {
				cancel()
				return runStartMsg{err: err, draft: text}
			}
			addon, err := attachments.BuildAddon(home, staged)
			if err != nil {
				cancel()
				return runStartMsg{err: err, draft: text}
			}
			images, err := attachments.BuildImages(home, staged)
			if err != nil {
				cancel()
				return runStartMsg{err: err, draft: text}
			}
			if ctx.Err() != nil {
				return runStartMsg{err: ctx.Err(), draft: text}
			}
			target.SetNextUserAddon(addon)
			target.SetNextUserImages(images)
			defer func() { target.SetNextUserAddon(""); target.SetNextUserImages(nil) }()
			selected = staged
			prompt += "\n\n" + attachmentDisplay(staged)
		}
		runPrompt := prompt
		if planMode {
			runPrompt = planmode.WrapPrompt(prompt)
		}
		previousSeq := 0
		if len(selected) > 0 && m.sessionStore != nil {
			previousSeq, _ = m.sessionStore.LatestMessageSeq(ctx, m.sessionID, "user")
		}
		if err := ctx.Err(); err != nil {
			cancel()
			return runStartMsg{err: err, draft: text}
		}
		ch, err := ag.Run(ctx, runPrompt)
		if err != nil {
			cancel()
			return runStartMsg{err: err, draft: text}
		}
		warning := ""
		if len(selected) > 0 && m.sessionStore != nil {
			seq, e := m.sessionStore.LatestMessageSeq(ctx, m.sessionID, "user")
			if e == nil && seq > previousSeq {
				e = m.sessionStore.SaveMessageAttachments(ctx, m.sessionID, seq, selected)
			} else if e == nil {
				e = fmt.Errorf("no persisted user message for attachment history")
			}
			if e != nil {
				warning = e.Error()
			}
		}
		return runStartMsg{ch: ch, mentionCount: mentionCount, mentionTokens: mentionTokens, attachmentsSent: len(selected) > 0, attachmentWarning: warning}
	}
}
