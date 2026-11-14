package routes

import (
	"sort"

	"gateway/internal/textutil"
)

// Table renders routes as "path -> backend" lines, sorted by path.
func Table(rs []Route, width int) []string {
	var lines []string
	for _, r := range rs {
		lines = append(lines, textutil.Truncate(r.Path+" -> "+r.Backend, width))
	}
	sort.Strings(lines)
	return lines
}
