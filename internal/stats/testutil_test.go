package stats

import "time"

// parseTime is a small helper for tests.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
