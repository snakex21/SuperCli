package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

// wakeRunner is implemented by *agent.Loop: an automatic turn for finished
// background workers that appends no user message.
type wakeRunner interface {
	RunWake(ctx context.Context, requireUnseen bool) (<-chan agent.Event, error)
}

type wakeSignaler interface {
	WakeScheduler() *agent.WakeScheduler
	UnseenBackgroundReports() int
}

// handleBackgroundExternal consumes the wake-up events that arrive on the
// external sink. ok=false means the event is not a wake-up event.
func (m Model) handleBackgroundExternal(ev agent.Event) (Model, tea.Cmd, bool) {
	switch e := ev.(type) {
	case agent.BackgroundAgentsEvent:
		m.backgroundAgents = e.Active
		return m, nil, true
	case agent.BackgroundWakeHeldEvent:
		if !e.First {
			return m, nil, true // same hold, only the waiting count changed
		}
		m.appendLine(m.palette.InputHint.Render(fmt.Sprintf(m.tr("tui.wake.held"), e.Unseen, e.Limit)))
		m.refreshTranscript()
		return m, nil, true
	case agent.BackgroundWakeEvent:
		next, cmd := m.startBackgroundWake(e)
		return next, cmd, true
	}
	return m, nil, false
}

// startBackgroundWake starts an automatic turn when the conversation is idle.
// A busy TUI ignores the request: the running turn sees the reports, or its
// end re-signals the scheduler (runEndMsg).
func (m Model) startBackgroundWake(e agent.BackgroundWakeEvent) (Model, tea.Cmd) {
	runner, ok := m.agent.(wakeRunner)
	if !ok || m.busy || m.mode == modeAsking || m.submittingDraft != "" {
		return m, nil
	}
	if m.onRunStart != nil {
		m.onRunStart()
	}
	ctx, cancel := context.WithCancel(llm.WithOpenCodeSession(context.Background(), m.sessionID))
	ctx = llm.WithCallSink(ctx, m.sessionUsageSink())
	m.cancel.Arm(cancelRun, cancel)
	m.appendLine(m.palette.InputHint.Render(fmt.Sprintf(m.tr("tui.wake.started"), max(1, e.Unseen))))
	m.markTranscriptPresent()
	m.busy = true
	m.clearRetryState()
	m.resetCurrent()
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, func() tea.Msg {
		ch, err := runner.RunWake(ctx, true)
		if err != nil {
			cancel()
			return runStartMsg{err: err, quiet: errors.Is(err, agent.ErrNothingToWake)}
		}
		return runStartMsg{ch: ch}
	}
}

// resignalBackgroundWake covers a wake request that arrived while the TUI
// still showed the previous turn as running.
func (m Model) resignalBackgroundWake() {
	if s, ok := m.agent.(wakeSignaler); ok && s.UnseenBackgroundReports() > 0 {
		s.WakeScheduler().Signal()
	}
}
