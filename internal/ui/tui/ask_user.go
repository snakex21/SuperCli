package tui

import (
	"fmt"
	"strings"
)

// renderAskView produces the full-screen overlay shown when the
// model has called ask_user. The viewport is suspended; the
// user navigates with 1-4 / arrows / space / enter / esc.
func renderAskView(a *pendingAsk, width, height int) string {
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	// Center the box horizontally.
	boxW := width - 4
	if boxW > 90 {
		boxW = 90
	}
	leftPad := (width - boxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	pad := strings.Repeat(" ", leftPad)
	hr := strings.Repeat("─", boxW)

	var b strings.Builder

	// Top border.
	b.WriteString(pad)
	b.WriteString("┌")
	b.WriteString(hr)
	b.WriteString("┐\n")

	// Header / question.
	b.WriteString(pad)
	b.WriteString("│ ")
	writeCentered(&b, headerLine(a), boxW-2)
	b.WriteString(" │\n")

	b.WriteString(pad)
	b.WriteString("│ ")
	writeCentered(&b, "", boxW-2)
	b.WriteString(" │\n")

	// Question, word-wrapped.
	for _, line := range wrap(a.Question, boxW-4) {
		b.WriteString(pad)
		b.WriteString("│   ")
		b.WriteString(padTo(line, boxW-4))
		b.WriteString("   │\n")
	}

	// Options label.
	b.WriteString(pad)
	b.WriteString("│ ")
	writeCentered(&b, "", boxW-2)
	b.WriteString(" │\n")

	// Options list.
	for i, opt := range a.Options {
		b.WriteString(pad)
		b.WriteString("│  ")
		marker := "  "
		isFocused := i == a.cursor
		checked := a.MultiSelect && a.toggled[i]
		switch {
		case a.MultiSelect && checked && isFocused:
			marker = "▶☑"
		case a.MultiSelect && checked:
			marker = " ☑"
		case a.MultiSelect && isFocused:
			marker = "▶☐"
		case isFocused:
			marker = "▶ "
		}
		label := fmt.Sprintf("%d. %s %s", i+1, marker, opt.Label)
		b.WriteString(padTo(label, boxW-4))
		b.WriteString("  │\n")
		if opt.Description != "" {
			b.WriteString(pad)
			b.WriteString("│    ")
			b.WriteString(padTo(opt.Description, boxW-6))
			b.WriteString("    │\n")
		}
		if opt.Preview != "" {
			b.WriteString(pad)
			b.WriteString("│    ")
			b.WriteString(padTo("preview: "+opt.Preview, boxW-6))
			b.WriteString("    │\n")
		}
		if opt.Image != "" {
			b.WriteString(pad)
			b.WriteString("│    ")
			b.WriteString(padTo("image: "+opt.Image, boxW-6))
			b.WriteString("    │\n")
		}
		if opt.ImagePrompt != "" && opt.Image == "" {
			b.WriteString(pad)
			b.WriteString("│    ")
			b.WriteString(padTo("prompt: "+opt.ImagePrompt, boxW-6))
			b.WriteString("    │\n")
		}
	}
	if a.customMode {
		b.WriteString(pad)
		b.WriteString("│  ")
		b.WriteString(padTo("Your answer: "+a.custom+"_", boxW-4))
		b.WriteString("  │\n")
	}

	// Bottom padding.
	b.WriteString(pad)
	b.WriteString("│ ")
	writeCentered(&b, "", boxW-2)
	b.WriteString(" │\n")

	// Help line.
	help := helpLine(a)
	b.WriteString(pad)
	b.WriteString("│ ")
	writeCentered(&b, help, boxW-2)
	b.WriteString(" │\n")

	// Bottom border.
	b.WriteString(pad)
	b.WriteString("└")
	b.WriteString(hr)
	b.WriteString("┘\n")

	return b.String()
}

func headerLine(a *pendingAsk) string {
	if a.Header != "" {
		return "[" + a.Header + "]"
	}
	return "?"
}

func helpLine(a *pendingAsk) string {
	if a.customMode {
		return "type answer · ⏎ submit · esc options"
	}
	custom := ""
	if a.AllowCustom {
		custom = " · c custom"
	}
	if a.MultiSelect {
		return "1-4 toggle · ↑↓ move · ⏎ confirm" + custom + " · esc cancel"
	}
	return "1-4 quick pick · ↑↓ move · ⏎ confirm" + custom + " · esc cancel"
}

// writeCentered writes s centered in width characters, padding
// with spaces. If s is longer than width, it is left as-is.
func writeCentered(b *strings.Builder, s string, width int) {
	if len(s) >= width {
		b.WriteString(s[:width])
		return
	}
	left := (width - len(s)) / 2
	right := width - len(s) - left
	b.WriteString(strings.Repeat(" ", left))
	b.WriteString(s)
	b.WriteString(strings.Repeat(" ", right))
}

// padTo right-pads s with spaces to width. If s is longer, it
// is truncated to width.
func padTo(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// wrap word-wraps s to width, splitting on spaces. It is
// intentionally simple (no hyphenation, no zero-width chars).
func wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			out = append(out, line)
			line = w
		} else {
			line += " " + w
		}
	}
	out = append(out, line)
	return out
}
