// Package render substitutes {{name}} placeholders.
package render

import (
	"strings"

	"tmplsvc/config"
)

// Render fills tmpl for page using c.Vars.
// TODO(templates): unbalanced braces are passed through verbatim.
func Render(c config.Config, page, tmpl string) string {
	applyDefaults(c, page)
	var b strings.Builder
	for {
		i := strings.Index(tmpl, "{{")
		if i < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		j := strings.Index(tmpl[i:], "}}")
		if j < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		b.WriteString(tmpl[:i])
		key := strings.TrimSpace(tmpl[i+2 : i+j])
		if v, ok := c.Vars[key]; ok {
			b.WriteString(v)
		} else if !c.Strict {
			b.WriteString(tmpl[i : i+j+2])
		}
		tmpl = tmpl[i+j+2:]
	}
}
