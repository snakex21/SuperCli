package tui

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
)

func (c *chat) addDocument(text string) {
	c.msgs = append(c.msgs, msg{role: roleDocument, text: text})
	c.completedDirty = true
}

func renderCommandDocument(text string, p Palette, width int) string {
	var out strings.Builder
	code := false
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			code = !code
			continue
		}
		if code {
			out.WriteString(p.MdCode.Render(line))
			continue
		}
		heading := strings.TrimLeft(line, "#")
		if len(heading) != len(line) && strings.HasPrefix(heading, " ") {
			out.WriteString(p.PanelTitle.Render(applyInlineMarkdown(strings.TrimSpace(heading), p)))
		} else {
			out.WriteString(renderMarkdownLine(line, p))
		}
	}
	return ansi.Wrap(out.String(), maxInt(1, width), "")
}
