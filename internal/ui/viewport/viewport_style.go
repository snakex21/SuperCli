package viewport

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
)

// A sparse checkpoint records the style entering a line. Balanced, single-line
// styling needs no entries. Checkpoints are immutable across copied models.
type lineStyle struct {
	line   int
	style  cellbuf.Style
	replay string
}

func (m *Model) updateLineStyles(lines []string, unchanged int) {
	end := sort.Search(len(m.lineStyles), func(i int) bool { return m.lineStyles[i].line > unchanged })
	if unchanged == len(lines) {
		m.lineStyles = m.lineStyles[:end]
		return
	}
	var style cellbuf.Style
	replay := ""
	if end > 0 {
		style = m.lineStyles[end-1].style
		replay = m.lineStyles[end-1].replay
	}
	changes := m.lineStyles[:end]
	detached := false
	var parser *ansi.Parser
	for i := unchanged; i < len(lines); i++ {
		line := lines[i]
		if strings.IndexByte(line, 0x1b) < 0 && strings.IndexByte(line, 0x9b) < 0 {
			continue
		}
		if parser == nil {
			parser = ansi.GetParser()
		}
		parser.Reset()
		var state byte
		for len(line) > 0 {
			sequence, _, n, next := ansi.DecodeSequence(line, state, parser)
			if ansi.HasCsiPrefix(sequence) && parser.Command() == 'm' {
				cellbuf.ReadStyle(parser.Params(), &style)
			}
			line, state = line[n:], next
		}
		sequence := ""
		if !style.Empty() {
			sequence = style.Sequence()
		}
		// Preserve terminal palette references versus explicit RGB values,
		// even when their nominal RGBA values happen to be equal.
		if sequence != replay {
			replay = sequence
			// Most tail edits retain every checkpoint. Reuse them until a
			// different transition requires independently owned storage.
			if !detached && len(changes) < len(m.lineStyles) {
				old := &m.lineStyles[len(changes)]
				if old.line == i+1 && replay == old.replay {
					changes = m.lineStyles[:len(changes)+1]
					continue
				}
			}
			if !detached {
				changes = append([]lineStyle(nil), changes...)
				detached = true
			}
			changes = append(changes, lineStyle{line: i + 1, style: style, replay: replay})
		}
	}
	if parser != nil {
		ansi.PutParser(parser)
	}
	m.lineStyles = changes
}

// Reopen inherited SGR before clipping each visible row and close it before
// frame/padding output. Work is bounded by visible rows, never by scroll depth.
func (m Model) renderLines(top, bottom int, horizontal bool) []string {
	lines := m.lines[top:bottom]
	width := m.Width - m.Style.GetHorizontalFrameSize()
	cut := horizontal && width != 0 && (m.xOffset != 0 || m.longestLineWidth > width)
	if len(lines) == 0 || (len(m.lineStyles) == 0 && !cut) {
		return lines
	}
	next := sort.Search(len(m.lineStyles), func(i int) bool { return m.lineStyles[i].line > top })
	replay := ""
	if next > 0 {
		replay = m.lineStyles[next-1].replay
	}
	var rendered []string
	for i, raw := range lines {
		line := raw
		if replay != "" {
			line = replay + line
		}
		if next < len(m.lineStyles) && m.lineStyles[next].line == top+i+1 {
			replay = m.lineStyles[next].replay
			next++
		}
		if cut {
			line = ansi.Cut(line, m.xOffset, m.xOffset+width)
		}
		if replay != "" {
			line += ansi.ResetStyle
		}
		if line != raw {
			if rendered == nil {
				rendered = append([]string(nil), lines...)
			}
			rendered[i] = line
		}
	}
	if rendered != nil {
		return rendered
	}
	return lines
}
