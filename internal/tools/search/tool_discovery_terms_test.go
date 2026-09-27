package search

import "testing"

func TestCommandDiscoveryKeepsUnrelatedTerms(t *testing.T) {
	for _, word := range []string{"status", "process", "analysis", "canvas", "windows", "news", "testing", "builder"} {
		if got := lexTokens(word); len(got) != 1 || got[0] != word {
			t.Fatalf("unrelated word %q changed: %v", word, got)
		}
	}
}
