package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	role      role
	text      string
	collapsed bool
}

// chat holds the ordered message history and the current
// streaming assistant text. It replaces the old raw
// transcript strings.Builder approach with structured
// messages that can be colored per-role.
type chat struct {
	msgs     []msg
	current  string // streaming assistant text (flushed on DoneEvent)
	width    int    // terminal width for word-wrapping
	language string

	// completedCache is the rendered, immutable prefix of completed messages.
	// Streaming used to run Markdown/ANSI rendering over the entire conversation
	// for every provider delta. Only current changes while a response streams,
	// so keep the completed prefix until a message/fold setting really changes.
	completedCache string
	completedDirty bool

	// thinkingCollapsed toggles <thinking> block visibility.
	// Press 'T' to expand/collapse all thinking blocks.
	thinkingCollapsed bool
}

// newChat creates an empty chat with the given terminal width.
func newChat(width int, language ...string) chat {
	lang := "en"
	if len(language) > 0 {
		lang = normalizeLanguage(language[0])
	}
	return chat{width: width, language: lang}
}

// addUser appends a user prompt.
func (c *chat) addUser(text string) {
	c.msgs = append(c.msgs, msg{role: roleUser, text: text})
	c.completedDirty = true
}

// addSystem appends a system message (markers, errors, etc).
func (c *chat) addSystem(text string) {
	c.msgs = append(c.msgs, msg{role: roleSystem, text: text})
	c.completedDirty = true
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

// removeLastSystem removes the most recent system message whose
// text equals the given string. Used to clear the transient
// "running · Ctrl+C to abort" marker once a slash command's
// result arrives, so finished commands don't read as still busy.
func (c *chat) removeLastSystem(text string) bool {
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].role == roleSystem && c.msgs[i].text == text {
			c.msgs = append(c.msgs[:i], c.msgs[i+1:]...)
			c.completedDirty = true
			return true
		}
	}
	return false
}

// addAssistant appends a completed assistant message.
func (c *chat) addAssistant(text string) {
	c.msgs = append(c.msgs, msg{role: roleAssistant, text: text})
	c.completedDirty = true
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
		c.completedDirty = true
	}
}

// render produces the full colored transcript. Each message
// is prefixed with a role label and colored with the palette.
func (c *chat) render(p Palette) string {
	var b strings.Builder
	b.WriteString(c.renderCompleted(p))
	// Render the current streaming message with a trailing newline
	// so the viewport cursor stays below it.
	if c.current != "" {
		if len(c.msgs) > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(renderAssistantMarkdown(c.current, p, c.thinkingCollapsed, c.language))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderMsg renders a single message with role-based color.
func (c *chat) renderMsg(m msg, p Palette) string {
	if m.collapsed {
		first := strings.TrimSpace(strings.SplitN(m.text, "\n", 2)[0])
		if first == "" {
			first = textFor(c.language, "(collapsed block)", "(zwini\u0119ty blok)")
		}
		return p.Dim.Render(first + textFor(c.language, "  \u2026 collapsed", "  \u2026 zwini\u0119te"))
	}
	switch m.role {
	case roleUser:
		// The plain transcript keeps a "> " prefix for compatibility; the
		// colored gutter already communicates the role visually.
		body := strings.TrimPrefix(m.text, "> ")
		return renderRoleBlock(p.UserLabel.Render(textFor(c.language, "You", "Ty")), p.User.Render(body), p.UserGutter)
	case roleAssistant:
		return renderRoleBlock(p.AssistantLabel.Render("SuperCli"), renderAssistantMarkdown(m.text, p, c.thinkingCollapsed, c.language), p.AssistGutter)
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

// renderRoleBlock renders a labeled message with a colored
// left-border gutter ("▌") instead of a heavy box — the gutter
// color identifies the speaker at a glance.
func renderRoleBlock(label, body string, gutter lipgloss.Style) string {
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
		b.WriteString(gutter.Render("▌") + " ")
		b.WriteString(line)
	}
	return b.String()
}

// renderWithSpinner renders the transcript including the current
// streaming text with a spinner appended.
func (c *chat) renderWithSpinner(p Palette, spinnerView string) string {
	var b strings.Builder
	b.WriteString(c.renderCompleted(p))
	if c.current != "" {
		if len(c.msgs) > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(renderRoleBlock(p.AssistantLabel.Render("SuperCli"), renderAssistantMarkdown(c.current, p, c.thinkingCollapsed, c.language), p.AssistGutter))
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

// renderCompleted keeps operational system events compact while adding one
// calm line of whitespace at real conversational boundaries.
func (c *chat) renderCompleted(p Palette) string {
	if !c.completedDirty {
		return c.completedCache
	}
	var b strings.Builder
	for i, m := range c.msgs {
		if i > 0 && (m.role == roleUser || m.role == roleAssistant) {
			b.WriteByte('\n')
		}
		b.WriteString(c.renderMsg(m, p))
		b.WriteByte('\n')
	}
	c.completedCache = b.String()
	c.completedDirty = false
	return c.completedCache
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
	c.completedDirty = true
}

// transcriptMatch is presentation-only search metadata. MessageIndex remains
// stable until a new completed message is appended; the search menu is modal,
// so the transcript cannot change underneath it.
type transcriptMatch struct {
	MessageIndex int
	Role         role
	Preview      string
}

// search returns messages containing query without rendering Markdown or ANSI.
// This is intentionally local and allocation-bounded by the number of messages;
// it never touches the model context or session database.
func (c *chat) search(query string) []transcriptMatch {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	matches := make([]transcriptMatch, 0, 8)
	for i, m := range c.msgs {
		plain := strings.Join(strings.Fields(m.text), " ")
		if !strings.Contains(strings.ToLower(plain), q) {
			continue
		}
		matches = append(matches, transcriptMatch{MessageIndex: i, Role: m.role, Preview: plain})
	}
	return matches
}

func (c *chat) isFoldable(index int) bool {
	return index >= 0 && index < len(c.msgs) && strings.Contains(c.msgs[index].text, "\n")
}

func (c *chat) toggleMessage(index int) bool {
	if !c.isFoldable(index) {
		return false
	}
	c.msgs[index].collapsed = !c.msgs[index].collapsed
	c.completedDirty = true
	return c.msgs[index].collapsed
}

// renderedLineForMessage resolves a raw message index to its first rendered
// terminal row. It is paid only when the user selects a search result, never on
// redraw or streaming paths.
func (c *chat) renderedLineForMessage(index int, p Palette) int {
	if index <= 0 {
		return 0
	}
	if index > len(c.msgs) {
		index = len(c.msgs)
	}
	lines := 0
	for i := 0; i < index; i++ {
		if i > 0 && (c.msgs[i].role == roleUser || c.msgs[i].role == roleAssistant) {
			lines++
		}
		rendered := c.renderMsg(c.msgs[i], p)
		lines += strings.Count(rendered, "\n") + 1
	}
	return lines
}
