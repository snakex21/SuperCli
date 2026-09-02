// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

func (m Model) handleAgentEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case agent.MessageEvent:
		m.current += e.Text
		m.responseLen += len(e.Text)
		m.chat.appendCurrent(e.Text)
		m.refreshTranscript()
		m.viewport.GotoBottom()
		return m, m.waitForNextEvent()
	case agent.ReasoningEvent:
		text := "<thinking>" + e.Text + "</thinking>\n"
		m.current += text
		m.responseLen += len(text)
		m.chat.appendCurrent(text)
		m.refreshTranscript()
		m.viewport.GotoBottom()
		return m, m.waitForNextEvent()
	case agent.ToolCallEvent:
		m.lastToolName = e.Name
		m.toolActivity.call(e.Name, e.Args)
		line := m.marker.ToolCall(e.Name, e.Args)
		m.appendLine(line)
		return m, m.waitForNextEvent()
	case agent.ToolResultEvent:
		if e.Err != nil {
			m.toolActivity.errors++
			m.appendLine(m.marker.ToolResultErr(m.lastToolName, e.Err.Error()))
		} else {
			m.appendLine(m.marker.ToolResultFull(m.lastToolName, e.Output, m.toolExpanded))
		}
		return m, m.waitForNextEvent()
	case agent.DoneEvent:
		// Save response text before flush for token estimation.
		responseText := m.current
		m.flushCurrent()
		in, out := e.Usage.Input, e.Usage.Output
		estimated := false
		if in == 0 && out == 0 && len(responseText) > 0 {
			// Provider didn't report usage (e.g. LM Studio).
			// Use tiktoken-go for GPT models, chars/3 for Qwen/Llama,
			// chars/4 as universal fallback.
			modelName := ""
			if m.llm != nil {
				modelName = m.llm.Name()
			}
			out, estimated = llm.CountTokensEstimate(responseText, modelName)
			in = out / 2 // rough input estimate
			if in == 0 {
				in = 1
			}
		}
		if m.toolActivity.calls > 0 {
			m.chat.addSystem(m.marker.ToolActivity(m.toolActivity.calls, m.toolActivity.errors, m.toolActivity.repeats, m.toolActivity.byName))
		}
		m.chat.addSystem(m.marker.DoneEst(in, out, estimated))
		m.refreshRuntimeHUD()
		m.appendLineToTranscript(fmt.Sprintf("(done · %d in / %d out)", in, out))
		return m, func() tea.Msg { return runEndMsg{} }
	case agent.ErrorEvent:
		m.flushCurrent()
		if m.toolActivity.calls > 0 {
			m.chat.addSystem(m.marker.ToolActivity(m.toolActivity.calls, m.toolActivity.errors, m.toolActivity.repeats, m.toolActivity.byName))
		}
		err := e.Err
		// A4: a retired/unknown model returns a raw provider
		// error. Surface it as an actionable message instead —
		// and never touch the model registry, so the /model
		// picker keeps working after the failure.
		if isModelUnavailableErr(err) {
			name := "current model"
			if m.llm != nil {
				name = m.llm.Name()
			}
			err = fmt.Errorf("model %q is unavailable on this provider — pick another with /model (%v)", name, e.Err)
		}
		m.chat.addSystem(m.marker.Error(err))
		m.appendLineToTranscript(fmt.Sprintf("(error: %v)", err))
		return m, func() tea.Msg { return runEndMsg{} }
	case agent.DraftUsedEvent:
		line := m.marker.Draft(e.DraftModel, e.VerifierModel, e.Savings, e.Decision)
		m.appendLine(line)
		m.appendLineToTranscript(fmt.Sprintf("[draft: %s → %s, saved %d tokens]", e.DraftModel, e.VerifierModel, e.Savings))
		return m, m.waitForNextEvent()
	case agent.AutoCompactEvent:
		line := fmt.Sprintf("[auto-compact: %d message(s) compacted (%s, ~%d/%d tokens, trigger=%d/%s, estimate=%s, window=%s)]",
			e.Removed, e.Reason, e.Estimated, e.Window, e.Threshold, e.ThresholdSource, e.EstimateSource, e.WindowSource)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	case agent.ToolResultsPrunedEvent:
		line := fmt.Sprintf("[prune: %d old tool result(s) → reclaimed ~%d tokens (~%d/%d, trigger=%d/%s)]",
			e.Pruned, e.Reclaimed, e.Estimated, e.Window, e.Threshold, e.ThresholdSource)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	case agent.MessagesHiddenEvent:
		line := m.marker.ContextHid(e.Count, e.Reason)
		m.appendLine(line)
		m.appendLineToTranscript(fmt.Sprintf("[context: hid %d message(s)]", e.Count))
		return m, m.waitForNextEvent()
	case agent.ConsultEvent:
		if e.AllFailed {
			m.appendLine(m.marker.CouncilAllFailed())
			return m, m.waitForNextEvent()
		}
		m.appendLine(m.marker.Council(e.CandidateCount, e.WinnerProvider, e.Reason))
		m.appendLine(m.marker.CouncilQuestion(e.Question))
		return m, m.waitForNextEvent()
	case agent.WorkerNotificationEvent:
		line := fmt.Sprintf("[worker %s: %s] %s", e.TaskID, e.Status, e.Summary)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	case agent.NoticeEvent:
		// Provider status (e.g. rate-limit retry wait) — show it so
		// the user knows the run is waiting, not hung.
		line := fmt.Sprintf("[%s]", e.Text)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	}
	return m, m.waitForNextEvent()
}

// refreshTranscript rewrites the viewport with the colored chat
// and the streaming current text with optional spinner.
func (m *Model) refreshTranscript() {
	// Sync the chat's streaming text with the model's current.
	m.chat.current = m.current
	spinnerView := ""
	if m.busy && m.current != "" {
		spinnerView = m.spinner.View()
	}
	content := m.chat.renderWithSpinner(m.palette, spinnerView)
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *Model) flushCurrent() {
	if m.current != "" {
		m.chat.addAssistant(m.current)
		m.appendLineToTranscript(m.current)
		m.current = ""
	}
}

// appendLine adds a system-level message to the chat and the
// raw transcript for backward-compatible test assertions.
func (m *Model) appendLine(line string) {
	m.chat.addSystem(line)
	m.appendLineToTranscript(line)
}

// appendLineToTranscript writes to the raw transcript only.
// Used for plain-text markers that don't need chat coloring.
func (m *Model) appendLineToTranscript(line string) {
	m.transcript.WriteString(line)
	m.transcript.WriteByte('\n')
}

func (m *Model) completedLines() string {
	return m.transcript.String()
}

// View renders the current state. When the user is being
// asked a question, the ask UI takes over the full screen.
//
// Layout (top to bottom):
//   - chat area (scrollable viewport)
//   - separator line (dim)
//   - status bar (credits, goal, model)
//   - input line ( "> " prompt + text input)
