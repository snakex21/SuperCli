package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// A lossless, uncapped editor buffer. In particular, neither pasted content nor
// existing GUI presets inherit a widget's character/line limits or sanitization.
// Only the viewport is clipped; the complete buffer is always saved.
type instructionBuffer struct {
	text         []rune
	at           int
	goalColumn   int
	goalWidth    int
	verticalGoal bool
}

func newInstructionBuffer(text string) instructionBuffer {
	runes := []rune(text)
	return instructionBuffer{text: runes, at: len(runes)}
}

func (b instructionBuffer) value() string { return string(b.text) }

func (b *instructionBuffer) replace(start, end int, text []rune) {
	next := make([]rune, 0, len(b.text)-(end-start)+len(text))
	next = append(next, b.text[:start]...)
	next = append(next, text...)
	b.at = len(next)
	b.text = append(next, b.text[end:]...)
	b.verticalGoal = false
}

func (b *instructionBuffer) insert(text string) { b.replace(b.at, b.at, []rune(text)) }

func (b instructionBuffer) lineStart() int {
	at := b.at
	for at > 0 && b.text[at-1] != '\n' {
		at--
	}
	return at
}

func (b instructionBuffer) lineEnd() int {
	at := b.at
	for at < len(b.text) && b.text[at] != '\n' {
		at++
	}
	return at
}

func (b *instructionBuffer) vertical(delta, width int) {
	rows, row, column := b.visualRows(width)
	if !b.verticalGoal || b.goalWidth != width {
		b.goalColumn, b.goalWidth, b.verticalGoal = column, width, true
	}
	target := maxInt(0, minInt(len(rows)-1, row+delta))
	if target == row {
		return
	}
	// Pick a rune boundary at or before the preferred display-cell column.
	// Retain that column across shorter rows and wide Unicode characters.
	b.at = rows[target][0].at
	for _, cell := range rows[target] {
		if cell.column > b.goalColumn {
			break
		}
		b.at = cell.at
	}
}

func (b *instructionBuffer) update(msg tea.KeyMsg, multiline bool, width, pageHeight int) {
	switch msg.String() {
	case "up":
		b.vertical(-1, width)
		return
	case "down":
		b.vertical(1, width)
		return
	case "pgup":
		b.vertical(-maxInt(1, pageHeight), width)
		return
	case "pgdown":
		b.vertical(maxInt(1, pageHeight), width)
		return
	}
	b.verticalGoal = false
	switch msg.String() {
	case "left":
		b.at = maxInt(0, b.at-1)
	case "right":
		b.at = minInt(len(b.text), b.at+1)
	case "home", "ctrl+a":
		b.at = b.lineStart()
	case "end", "ctrl+e":
		b.at = b.lineEnd()
	case "ctrl+home":
		b.at = 0
	case "ctrl+end":
		b.at = len(b.text)
	case "backspace", "ctrl+h":
		if b.at > 0 {
			b.replace(b.at-1, b.at, nil)
		}
	case "delete":
		if b.at < len(b.text) {
			b.replace(b.at, b.at+1, nil)
		}
	case "ctrl+u":
		b.replace(b.lineStart(), b.at, nil)
	case "ctrl+k":
		b.replace(b.at, b.lineEnd(), nil)
	case "ctrl+w":
		start := b.at
		for start > 0 && unicode.IsSpace(b.text[start-1]) {
			start--
		}
		for start > 0 && !unicode.IsSpace(b.text[start-1]) {
			start--
		}
		b.replace(start, b.at, nil)
	case "enter":
		if multiline {
			b.insert("\n")
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			text := string(msg.Runes)
			if msg.Type == tea.KeySpace {
				text = " "
			}
			if !multiline {
				text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
			}
			b.insert(text)
		}
	}
}

type instructionVisualCell struct {
	at, column int
	display    string
	hidden     bool // newline/end-of-buffer: display only when holding the caret
}

// One shared, caret-independent mapping drives both wrapping and navigation.
// Every rune boundary (including EOF) belongs to exactly one visual row.
// Controls are visible placeholders, never terminal escapes; storage is intact.
func (b instructionBuffer) visualRows(width int) ([][]instructionVisualCell, int, int) {
	width = maxInt(2, width)
	rows := [][]instructionVisualCell{{}}
	column, caretRow, caretColumn := 0, 0, 0
	for i := 0; i <= len(b.text); i++ {
		ch := ' '
		if i < len(b.text) {
			ch = b.text[i]
		}
		display := string(ch)
		switch {
		case ch == '\n':
			display = " "
		case ch == '\t':
			display = "    "
		case unicode.IsControl(ch):
			display = "�"
		}
		size := ansi.StringWidth(display)
		if size > width {
			display, size = "→", 1
		}
		if column+size > width {
			rows = append(rows, nil)
			column = 0
		}
		if i == b.at {
			caretRow, caretColumn = len(rows)-1, column
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], instructionVisualCell{
			at: i, column: column, display: display, hidden: ch == '\n' || i == len(b.text),
		})
		column += size
		if ch == '\n' {
			rows = append(rows, nil)
			column = 0
		}
	}
	return rows, caretRow, caretColumn
}

func (b instructionBuffer) view(width, height int, focused bool, palette Palette) string {
	height = maxInt(1, height)
	rows, caretRow, _ := b.visualRows(width)
	start := maxInt(0, caretRow-height+1)
	end := minInt(len(rows), start+height)
	visible := make([]string, 0, height)
	for _, row := range rows[start:end] {
		var line strings.Builder
		for _, cell := range row {
			caret := cell.at == b.at
			if cell.hidden && !caret {
				continue
			}
			display := cell.display
			if caret && focused {
				display = palette.InputText.Reverse(true).Render(display)
			}
			line.WriteString(display)
		}
		visible = append(visible, line.String())
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}
