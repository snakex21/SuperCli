package doctor

import (
	"fmt"
	"strings"
)

func RenderPlain(r Report) string {
	ok, warn, fail, skip := r.Summary()
	var b strings.Builder
	fmt.Fprintf(&b, "supercli %s — doctor\n", r.Version)
	fmt.Fprintf(&b, "%d ok · %d warn · %d fail · %d skip\n\n", ok, warn, fail, skip)
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "%s %-16s %s\n", plainMark(c.Status), c.Name, c.Detail)
		if c.Remediation != "" {
			fmt.Fprintf(&b, "   fix: %s\n", c.Remediation)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func plainMark(s Status) string {
	switch s {
	case OK:
		return "[ok]"
	case Warn:
		return "[!!]"
	case Fail:
		return "[xx]"
	case Skip:
		return "[--]"
	default:
		return "[??]"
	}
}
