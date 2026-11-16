package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/tools"
)

// The host approval prompt is a card above the transcript tail: details
// scroll inside it, Deny is focused first, approval is possible only after
// the details were read to the end, and direct keys work: y approve once,
// a approve similar in this session, n/Esc deny. Labels come from the trusted
// approval card, never from the model. All card chrome is localized.

// approvalLayout is computed identically for rendering and key handling, so
// "read to end" always refers to what the user actually saw.
type approvalLayout struct {
	rows       []string // wrapped, escaped detail rows
	slots      int      // detail rows visible at once
	counter    bool     // a "Details a-b of n" row is shown (scrolling needed)
	choicesRow bool     // all choices fit on one row
	inner      int      // content width inside the frame
	usable     bool
}

func (l approvalLayout) height() int {
	choices := 1
	if !l.choicesRow {
		choices = 3
	}
	h := 1 + 1 + 1 + l.slots + 1 + choices + 1 // top, title, badges, details, gap, choices, bottom
	if l.counter {
		h++
	}
	return h
}

func approvalLabels(a *pendingAsk) []string {
	labels := []string{a.Approval.DenyLabel, a.Approval.ApproveLabel}
	if a.Approval.SimilarLabel != "" {
		labels = append(labels, a.Approval.SimilarLabel)
	}
	return labels
}

// approvalTitle is the human question for the operation ("Remove a file
// outside the workspace?"), falling back to the gate header.
func approvalTitle(a *pendingAsk, language string) string {
	if op := a.Approval.Operation; op != "" {
		key := "approval.op." + op
		if text := textFor(language, key); text != key {
			return text
		}
	}
	if a.Header != "" {
		return a.Header
	}
	return textFor(language, "approval.header")
}

// approvalDetailText renders the structured card in the UI language. A card
// without a summary (older or synthetic gates) shows the gate's own question.
func approvalDetailText(a *pendingAsk, language string) string {
	c := a.Approval
	if strings.TrimSpace(c.Summary) == "" {
		return a.Question
	}
	var b strings.Builder
	summary := c.Summary
	if c.SummaryKey != "" {
		if text := textFor(language, c.SummaryKey); text != c.SummaryKey {
			pairs := make([]string, 0, 2*len(c.SummaryArgs))
			for name, value := range c.SummaryArgs {
				pairs = append(pairs, "{"+name+"}", value)
			}
			summary = strings.NewReplacer(pairs...).Replace(text)
			if c.SummaryDetail != "" {
				summary += "\n" + c.SummaryDetail
			}
		}
	}
	b.WriteString(summary)
	for _, w := range c.Warnings {
		b.WriteString("\n! " + textFor(language, "approval.field.warnings") + ": " + w)
	}
	type field struct{ label, value string }
	account := c.AccountName
	if c.Account != "" && !strings.EqualFold(c.AccountName, c.Account) {
		if account != "" {
			account += " (" + c.Account + ")"
		} else {
			account = c.Account
		}
	}
	operation := c.Tool
	if c.Operation != "" {
		operation += " / " + c.Operation
	}
	fields := []field{{"approval.field.tool", operation}, {"approval.field.account", account},
		{"approval.field.folder", c.Folder}, {"approval.field.destination", c.Destination}, {"approval.field.target", c.Target}}
	if c.Count >= 0 && (c.Count > 0 || len(c.Samples) > 0) {
		fields = append(fields, field{"approval.field.count", fmt.Sprint(c.Count)})
	}
	width := 0
	for _, f := range fields {
		if f.value != "" {
			width = max(width, ansi.StringWidth(textFor(language, f.label)))
		}
	}
	b.WriteString("\n")
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		label := textFor(language, f.label)
		b.WriteString("\n" + label + strings.Repeat(" ", width-ansi.StringWidth(label)+2) + f.value)
	}
	for _, s := range c.Samples {
		parts := []string{}
		for _, p := range []string{s.Date, s.From} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		subject := s.Subject
		if subject == "" {
			subject = textFor(language, "approval.no_subject")
		}
		parts = append(parts, "\""+subject+"\"")
		b.WriteString("\n  - " + strings.Join(parts, " · "))
	}
	return b.String()
}

func approvalChoice(a *pendingAsk, i int, canAllow bool, language string) string {
	keys := []string{"n", "y", "a"}
	label := approvalLabels(a)[i]
	if i > 0 && !canAllow {
		label = textFor(language, "tui.approval.readToEnd") + " " + label
	}
	marker := "  "
	if i == a.cursor {
		marker = "› "
	}
	return marker + "[" + keys[i] + "] " + escapeControls(label)
}

func (m Model) approvalLayout(a *pendingAsk) approvalLayout {
	if m.width < 24 || m.height < 10 {
		return approvalLayout{}
	}
	l := approvalLayout{inner: min(m.width, 92) - 4, usable: true}
	l.rows = wrap(escapeControls(approvalDetailText(a, m.language)), l.inner)
	one := make([]string, 0, 3)
	for i := range approvalLabels(a) {
		one = append(one, approvalChoice(a, i, false, m.language))
	}
	l.choicesRow = ansi.StringWidth(strings.Join(one, "   ")) <= l.inner
	// One header line of the application stays visible above the card.
	l.slots = len(l.rows)
	avail := m.height - 1 - l.height()
	if avail < 0 {
		l.counter = true
		l.slots = len(l.rows) + avail - 1
	}
	if l.slots < 1 {
		return approvalLayout{}
	}
	return l
}

// renderApprovalView shows the header, the transcript tail and the card,
// whose height follows its content instead of filling the screen.
func (m Model) renderApprovalView() string {
	a := m.pendingAsk
	l := m.approvalLayout(a)
	if !l.usable {
		message := wrap(m.tr("tui.approval.resize"), max(1, m.width))
		return strings.Join(message[:min(len(message), max(1, m.height))], "\n")
	}
	p := m.palette
	// Thin frame lines: the title inside the top edge carries the weight.
	frame := p.StatusPending.Bold(false)
	if !a.Approval.Reversible || a.Approval.Risk == "destructive" {
		frame = p.Error
	}
	inner := l.inner
	start := min(max(0, a.detailOffset), max(0, len(l.rows)-l.slots))
	end := min(len(l.rows), start+l.slots)
	canAllow := end == len(l.rows)

	var card []string
	edge := func(left, label, right string) string {
		label = truncateVisible(label, inner)
		fill := max(0, inner+1-ansi.StringWidth(label))
		return frame.Render(left+"─") + frame.Bold(true).Render(label) + frame.Render(strings.Repeat("─", fill)+right)
	}
	row := func(s string) {
		card = append(card, frame.Render("│")+" "+padTo(s, inner)+" "+frame.Render("│"))
	}
	card = append(card, edge("╭", " ⏸ "+escapeControls(m.tr("approval.header"))+" ", "╮"))
	row(p.Bold.Render(escapeControls(approvalTitle(a, m.language))))
	risk := string(a.Approval.Risk)
	if risk == "" {
		risk = "write_reversible"
	}
	riskStyle, revStyle, revKey := p.StatusPending, p.Success, "approval.badge.reversible"
	if risk == "destructive" {
		riskStyle = p.Error
	}
	if !a.Approval.Reversible {
		revStyle, revKey = p.Error, "approval.badge.irreversible"
	}
	row(riskStyle.Render(strings.ToUpper(m.tr("approval.risk."+risk))) + p.Dim.Render(" · ") + revStyle.Render(strings.ToUpper(m.tr(revKey))))
	for _, s := range l.rows[start:end] {
		row(s)
	}
	if l.counter {
		row(p.Dim.Render(fmt.Sprintf(m.tr("tui.approval.details"), start+1, end, len(l.rows))))
	}
	row("")
	choices := make([]string, 0, 3)
	for i := range approvalLabels(a) {
		text := approvalChoice(a, i, canAllow, m.language)
		style := p.Dim
		switch {
		case i == a.cursor:
			style = p.Header
		case i == 0 || canAllow:
			style = p.Assistant
		}
		choices = append(choices, style.Render(text))
	}
	if l.choicesRow {
		row(strings.Join(choices, "   "))
	} else {
		for i := 0; i < 3; i++ {
			if i < len(choices) {
				row(choices[i])
			} else {
				row("")
			}
		}
	}
	card = append(card, edge("╰", " "+fitKeyHint(m.tr("tui.approval.keys"), inner-2)+" ", "╯"))

	// The conversation stays visible above the card: header plus the tail
	// of the transcript viewport fill whatever the card does not need.
	out := []string{m.renderHeader()}
	if tail := m.height - 1 - len(card); tail > 0 {
		lines := strings.Split(m.viewport.View(), "\n")
		// A short conversation leaves blank rows under it; show its end.
		for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		for len(lines) < tail {
			lines = append([]string{""}, lines...)
		}
		out = append(out, lines...)
	}
	out = append(out, card...)
	if len(out) > m.height {
		out = out[len(out)-m.height:]
	}
	for i, line := range out {
		if lipgloss.Width(line) > m.width {
			out[i] = truncateVisible(line, m.width)
		}
	}
	return strings.Join(out, "\n")
}

func (m Model) handleApprovalKey(msg tea.KeyMsg, a *pendingAsk, canAllow bool, last, slots, rows int) (tea.Model, tea.Cmd) {
	labels := approvalLabels(a)
	choose := func(i int) {
		if i <= 0 || i >= len(labels) || !canAllow {
			safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		} else {
			safeRespond(a.respond, tools.AskAnswer{Selected: []string{labels[i]}})
		}
		m.endAsk()
	}
	switch msg.String() {
	case "esc", "ctrl+c", "n", "N":
		choose(0)
		return m, nil
	case "y", "Y":
		if canAllow {
			choose(1)
		}
		return m, nil
	case "a", "A":
		if canAllow && len(labels) > 2 {
			choose(2)
		}
		return m, nil
	case "enter":
		choose(a.cursor)
		return m, nil
	case "tab", "right":
		if canAllow {
			a.cursor = (a.cursor + 1) % len(labels)
		}
	case "shift+tab", "left":
		if canAllow {
			a.cursor = (a.cursor + len(labels) - 1) % len(labels)
		}
	case "up", "k":
		a.detailOffset = max(0, a.detailOffset-1)
	case "down", "j":
		a.detailOffset = min(last, a.detailOffset+1)
	case "pgup":
		a.detailOffset = max(0, a.detailOffset-max(1, slots-1))
	case "pgdown":
		a.detailOffset = min(last, a.detailOffset+max(1, slots-1))
	case "home":
		a.detailOffset = 0
	case "end":
		a.detailOffset = last
	}
	if a.detailOffset+slots < rows {
		a.cursor = 0
	}
	return m, nil
}

// fitKeyHint shortens a " · "-separated key hint to width by dropping its
// leading segments, so the trailing (most important) keys such as "y/n/a"
// stay readable instead of being cut off with an ellipsis.
func fitKeyHint(hint string, width int) string {
	parts := strings.Split(hint, " · ")
	for len(parts) > 1 && ansi.StringWidth(strings.Join(parts, " · ")) > width {
		parts = parts[1:]
	}
	return strings.Join(parts, " · ")
}
