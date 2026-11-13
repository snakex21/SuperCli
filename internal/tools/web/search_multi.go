package web

import (
	"fmt"
	"strings"
)

// formatMultiSearchResults renders several queries of one web_search call.
// Each query keeps its own label and engine; a URL already listed under an
// earlier query is counted instead of repeated, so parallel related queries
// do not inflate the result with duplicates.
func formatMultiSearchResults(queries []string, values []searchCacheValue, errs []error) string {
	var sb strings.Builder
	seen := map[string]bool{}
	fileCandidate := false
	for i, q := range queries {
		if i > 0 {
			sb.WriteString("\n")
		}
		if errs[i] != nil {
			fmt.Fprintf(&sb, "Query %d %q: search failed: %v\n", i+1, q, errs[i])
			continue
		}
		v := values[i]
		fmt.Fprintf(&sb, "Query %d %q (engine: %s", i+1, q, v.engine)
		if v.fallbackFrom != "" {
			fmt.Fprintf(&sb, ", fallback from %s", v.fallbackFrom)
		}
		sb.WriteString("):\n")
		shown, repeated := 0, 0
		for _, r := range v.results {
			key := strings.TrimSuffix(strings.ToLower(r.URL), "/")
			if seen[key] {
				repeated++
				continue
			}
			seen[key] = true
			shown++
			fmt.Fprintf(&sb, "%d. %s\n   %s\n", shown, r.Title, r.URL)
			if r.Snippet != "" {
				fmt.Fprintf(&sb, "   %s\n", r.Snippet)
			}
			fileCandidate = fileCandidate || hasDirectFileExtension(r.URL)
		}
		if shown == 0 && repeated == 0 {
			sb.WriteString("   (no results)\n")
		}
		if repeated > 0 {
			fmt.Fprintf(&sb, "   (%d more already listed above)\n", repeated)
		}
	}
	sb.WriteString(searchResultsFooter(fileCandidate))
	return sb.String()
}
