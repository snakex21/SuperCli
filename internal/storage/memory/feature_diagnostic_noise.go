package memory

import "strings"

// IsDiagnosticNoise identifies legacy machine-generated pattern entries whose
// only information is that the error classifier had no matching rule.
// Keep them on disk for inspection, but do not spend model context on them.
func IsDiagnosticNoise(e Entry) bool {
	return strings.HasPrefix(e.Scope, "pattern:") &&
		strings.Contains(strings.ToLower(e.Content), "no heuristic matched")
}
