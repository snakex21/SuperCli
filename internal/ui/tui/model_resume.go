package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
)

// The model projection belongs to the agent. The UI always reads the full
// persisted transcript, including reasoning and tools hidden from that projection.
type resumedTranscript struct {
	ID, Title    string
	Messages     []llm.Message
	ModelContext []llm.Message
	Discovered   []string
	Attachments  map[int][]string
	Seqs         []int
}

type resumeLoadedMsg struct {
	ctx     context.Context
	history *resumedTranscript
	err     error
}

func (m Model) resumeConversation(id string) (tea.Model, tea.Cmd) {
	store := m.sessionStore
	if store == nil {
		return m, func() tea.Msg { return slashResultMsg{Err: fmt.Errorf("session history unavailable")} }
	}
	m.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	m.resumeContext = ctx
	m.cancel.Arm(cancelRun, cancel)
	return m, func() tea.Msg {
		result := resumeLoadedMsg{ctx: ctx}
		sess, err := store.Get(id)
		if err != nil {
			result.err = err
			return result
		}
		if m.home != "" && sess.Cwd != "" {
			a, _ := filepath.Abs(m.home)
			b, _ := filepath.Abs(sess.Cwd)
			same := a == b
			if runtime.GOOS == "windows" {
				same = strings.EqualFold(a, b)
			}
			if !same {
				result.err = fmt.Errorf("%s: %s", m.tr("Switch to this project before continuing the session", "Przełącz projekt przed kontynuacją tej sesji"), sess.Cwd)
				return result
			}
		}
		rows, err := store.ReadMessages(ctx, id)
		if err != nil {
			result.err = err
			return result
		}
		if len(rows) == 0 {
			result.err = fmt.Errorf("session is empty")
			return result
		}
		history := &resumedTranscript{ID: id, Title: sess.Title}
		for _, row := range rows {
			message, err := row.ToMessage()
			if err != nil {
				result.err = err
				return result
			}
			history.Messages = append(history.Messages, message)
			history.Seqs = append(history.Seqs, row.Seq)
		}
		history.Attachments, err = store.ReadMessageAttachmentsRange(ctx, id, 0, 0)
		if err != nil {
			result.err = err
			return result
		}
		history.ModelContext, err = store.ModelContextFromTranscript(ctx, id, history.Messages, history.Seqs)
		if err != nil {
			result.err = err
			return result
		}
		history.Discovered, err = store.ReadDiscoveredTools(ctx, id)
		if err != nil {
			result.err = err
			return result
		}
		if m.prepareResume != nil {
			if err = m.prepareResume(ctx, id); err != nil {
				result.err = err
				return result
			}
		}
		result.history = history
		return result
	}
}

func (m Model) finishResume(msg resumeLoadedMsg) (tea.Model, tea.Cmd) {
	// A cancelled or superseded load is read-only and cannot change the agent.
	if msg.ctx != m.resumeContext || msg.ctx.Err() != nil {
		return m, nil
	}
	m.resumeContext = nil
	err := msg.err
	h := msg.history
	if err == nil && h != nil {
		switch {
		case m.resumeSession != nil:
			if err = m.sessionStore.PreserveLegacyUsage(msg.ctx, h.ID); err != nil {
				break
			}
			err = m.resumeSession(msg.ctx, h.ID, h.ModelContext, h.Discovered)
			if err == nil {
				m.sessionID = h.ID
			}
		case m.commands["resume"] != nil:
			_, err = SafeWrap("resume", m.commands["resume"])(msg.ctx, h.ID)
		default:
			if loader, ok := m.agent.(interface{ LoadConversation([]llm.Message) }); ok {
				loader.LoadConversation(h.ModelContext)
			} else {
				err = fmt.Errorf("session continuation unavailable")
			}
		}
	}
	m.busy = false
	m.cancel.Cancel()
	m.cancel.Disarm()
	if err != nil {
		m.appendLine(m.marker.Error(err))
		m.refreshTranscript()
	} else if h != nil {
		m.applyResumedTranscript(h)
		m.refreshRuntimeHUD()
	}
	m.syncInputHeight()
	m.input.Focus()
	return m, nil
}

func (m *Model) applyResumedTranscript(h *resumedTranscript) {
	collapsed := m.chat.thinkingCollapsed
	m.chat = newChat(m.width, m.language)
	m.chat.thinkingCollapsed = collapsed
	m.chat.toolsExpanded = m.toolExpanded
	m.resetCurrent()
	m.transcript = transcriptBuffer{}
	m.workerViews = nil
	names := make(map[string]string)
	for i, item := range h.Messages {
		text := transcriptMessageText(item)
		switch item.Role {
		case llm.RoleUser:
			if paths := h.Attachments[h.Seqs[i]]; len(paths) > 0 && !strings.Contains(text, "📎") && !strings.Contains(text, "[files]") {
				text += "\n" + attachmentDisplay(paths)
			}
			m.chat.addUser(text)
			m.appendLineToTranscript("> " + text)
		case llm.RoleAssistant:
			if strings.TrimSpace(text) != "" {
				m.chat.addAssistant(text)
				m.appendLineToTranscript(text)
			}
			for _, tc := range item.ToolCalls {
				names[tc.ID] = tc.Name
				m.appendLine(m.marker.ToolCall(tc.Name, string(tc.Arguments)))
			}
		case llm.RoleTool:
			name := names[item.ToolCallID]
			if name == "" {
				name = item.Name
			}
			if name == "" {
				name = "tool"
			}
			m.chat.addToolResult(name, text, "")
			m.appendLineToTranscript(text)
		}
	}
	m.loadedSessionID = h.ID
	title := h.Title
	if strings.TrimSpace(title) == "" {
		title = h.ID
	}
	m.setStatus(m.tr("Conversation loaded: ", "Wczytano rozmowę: ")+title, true)
	m.refreshTranscript()
	m.viewport.GotoBottom()
}

// Text parts and provider-native reasoning must not be flattened together.
func transcriptMessageText(item llm.Message) string {
	if len(item.Parts) == 0 {
		return item.Content
	}
	var b strings.Builder
	for _, part := range item.Parts {
		switch part.Type {
		case llm.PartTypeText:
			b.WriteString(part.Text)
		case llm.PartTypeReasoning:
			if part.Text != "" {
				b.WriteString("<thinking>")
				b.WriteString(part.Text)
				b.WriteString("</thinking>")
			}
		}
	}
	if b.Len() == 0 {
		return item.Content
	}
	return b.String()
}
