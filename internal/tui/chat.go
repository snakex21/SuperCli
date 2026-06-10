package tui

import (
	"strings"
)

// role identifies who produced a chat message. The role
// determines the color applied when rendering.
type role int

const (
	roleUser      role = iota // user prompt input
	roleAssistant             // assistant text (streamed)
	roleSystem                // system markers, errors, events
)

// msg is a single rendered line in the chat transcript.
// The role drives the color; the body is the raw text
// (no ANSI codes — styling is applied at render time).
type msg struct {
	role role
	text string
}

// chat holds the ordered message history and the current
// streaming assistant text. It replaces the old raw
// transcript strings.Builder approach with structured
// messages that can be colored per-role.
type chat struct {
	msgs    []msg
	current string // streaming assistant text (flushed on DoneEvent)
	width   int    // terminal width for word-wrapping

	// thinkingCollapsed toggles <thinking> block visibility.
	// Press 'T' to expand/collapse all thinking blocks.
	thinkingCollapsed bool
}

// newChat creates an empty chat with the given terminal width.
func newChat(width int) chat {
	return chat{width: width}
}

// addUser appends a user prompt.
func (c *chat) addUser(text string) {
	c.msgs = append(c.msgs, msg{role: roleUser, text: text})
}

// addSystem appends a system message (markers, errors, etc).
func (c *chat) addSystem(text string) {
	c.msgs = append(c.msgs, msg{role: roleSystem, text: text})
}

// lastAssistant returns the most recent completed assistant
// message ("" when none). The streaming current text counts
// once it is non-empty, so Ctrl+Y mid-stream copies what is
// visible.
func (c *chat) lastAssistant() string {
	if c.current != "" {
		return c.current
	}
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].role == roleAssistant {
			return c.msgs[i].text
		}
	}
	return ""
}

// addAssistant appends a completed assistant message.
func (c *chat) addAssistant(text string) {
	c.msgs = append(c.msgs, msg{role: roleAssistant, text: text})
}

// appendCurrent appends text to the streaming assistant message.
func (c *chat) appendCurrent(text string) {
	c.current += text
}

// flushCurrent moves the streaming text into the message list
// and clears current.
func (c *chat) flushCurrent() {
	if c.current != "" {
		c.msgs = append(c.msgs, msg{role: roleAssistant, text: c.current})
		c.current = ""
	}
}

// render produces the full colored transcript. Each message
// is prefixed with a role label and colored with the palette.
func (c *chat) render(p Palette) string {
	var b strings.Builder
	for _, m := range c.msgs {
		b.WriteString(c.renderMsg(m, p))
		b.WriteByte('\n')
	}
	// Render the current streaming message with a trailing newline
	// so the viewport cursor stays below it.
	if c.current != "" {
		b.WriteString(renderAssistantMarkdown(c.current, p, c.thinkingCollapsed))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderMsg renders a single message with role-based color.
func (c *chat) renderMsg(m msg, p Palette) string {
	switch m.role {
	case roleUser:
		return renderRoleBlock(p.UserLabel.Render("You"), p.User.Render(m.text))
	case roleAssistant:
		return renderRoleBlock(p.AssistantLabel.Render("SuperCli"), renderAssistantMarkdown(m.text, p, c.thinkingCollapsed))
	case roleSystem:
		// System messages already carry ANSI styling from
		// the Marker methods (p.Marker.Render, p.Dim.Render,
		// etc.). Wrapping them in p.System.Render() would
		// produce nested ANSI sequences. Return as-is.
		return m.text
	default:
		return m.text
	}
}

func renderRoleBlock(label, body string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return label
	}
	lines := strings.Split(body, "\n")
	var b strings.Builder
	b.WriteString(label)
	b.WriteByte('\n')
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  ")
		b.WriteString(line)
	}
	return b.String()
}

// renderWithSpinner renders the transcript including the current
// streaming text with a spinner appended.
func (c *chat) renderWithSpinner(p Palette, spinnerView string) string {
	var b strings.Builder
	for _, m := range c.msgs {
		b.WriteString(c.renderMsg(m, p))
		b.WriteByte('\n')
	}
	if c.current != "" {
		b.WriteString(renderRoleBlock(p.AssistantLabel.Render("SuperCli"), renderAssistantMarkdown(c.current, p, c.thinkingCollapsed)))
		b.WriteString(" ")
		b.WriteString(spinnerView)
		b.WriteByte('\n')
	} else if spinnerView != "" {
		// No streaming text yet, but spinner is active —
		// show the spinner on its own line.
		b.WriteString(spinnerView)
		b.WriteByte('\n')
	}
	return b.String()
}

// lastRole returns the role of the most recent message, or
// roleSystem if the chat is empty. Used by the model to
// determine how to classify the next incoming line.
func (c *chat) lastRole() role {
	if len(c.msgs) == 0 {
		return roleSystem
	}
	return c.msgs[len(c.msgs)-1].role
}

// len returns the number of completed messages.
func (c *chat) len() int {
	return len(c.msgs)
}

// toggleThinking flips the thinking block visibility.
func (c *chat) toggleThinking() {
	c.thinkingCollapsed = !c.thinkingCollapsed
}
