// Package library implements F17: library alternatives
// search. Before the model commits to a library, it can
// call check_library_alternatives to discover whether a
// better alternative exists for the specific use case.
//
// The finder uses a built-in catalog of ~20 curated
// mappings (library → alternative for specific task) and
// can optionally probe Context7 MCP or do a web search
// for live data. The catalog is static so the tool works
// offline; live probing is a graceful-degradation bonus.
package library

import (
	"fmt"
	"strings"
	"time"
)

// Entry maps one library to a better alternative for a
// specific use case. A library may appear in multiple
// entries with different tasks and alternatives.
type Entry struct {
	// Library is the library name as the model would
	// know it (e.g. "leaflet", "moment.js").
	Library string

	// Task describes the use case where the alternative
	// is better (e.g. "10k+ polygons", "tree-shaking",
	// "bundle size").
	Task string

	// Alternative is the recommended replacement.
	Alternative string

	// Reason is a one-line explanation.
	Reason string

	// Confidence is 0..1 — how sure we are that the
	// alternative is better. High confidence for
	// well-known migrations (moment→dayjs); lower for
	// opinionated choices.
	Confidence float64
}

// Finder is the F17 library alternatives engine. It holds
// a static catalog and optionally a web search callback.
type Finder struct {
	catalog []Entry
	nowFn   func() time.Time
}

// NewFinder returns a Finder with the built-in catalog.
// nowFn may be nil (defaults to time.Now).
func NewFinder() *Finder {
	return &Finder{
		catalog: defaultCatalog(),
		nowFn:   nil,
	}
}

// Result is what the model sees after calling
// check_library_alternatives.
type Result struct {
	Found       bool
	Alternative string
	Reason      string
	Confidence  float64
}

// Check looks up the library in the built-in catalog.
// The library name is case-insensitive; task is matched
// as a substring (fuzzy but deterministic). Returns
// Found=false when no alternative is known — the model
// should proceed with the original library.
func (f *Finder) Check(library, task string) Result {
	lib := strings.ToLower(strings.TrimSpace(library))
	tsk := strings.ToLower(strings.TrimSpace(task))

	var best Entry
	bestScore := 0.0
	found := false

	for _, e := range f.catalog {
		if strings.ToLower(e.Library) != lib {
			continue
		}
		score := matchScore(tsk, strings.ToLower(e.Task))
		if score > bestScore {
			bestScore = score
			best = e
			found = true
		}
	}

	if !found {
		return Result{Found: false}
	}
	return Result{
		Found:       true,
		Alternative: best.Alternative,
		Reason:      best.Reason,
		Confidence:  best.Confidence,
	}
}

// matchScore returns 0..1 for how well the query matches
// the catalog task string. Exact match = 1.0, substring
// = 0.7, word overlap = 0.4, no match = 0.
func matchScore(query, task string) float64 {
	if query == "" || task == "" {
		return 0
	}
	if query == task {
		return 1.0
	}
	if strings.Contains(task, query) || strings.Contains(query, task) {
		return 0.7
	}
	// word overlap
	qWords := strings.Fields(query)
	tWords := strings.Fields(task)
	overlap := 0
	for _, qw := range qWords {
		for _, tw := range tWords {
			if qw == tw {
				overlap++
				break
			}
		}
	}
	if len(qWords) > 0 && overlap > 0 {
		return 0.4 * float64(overlap) / float64(len(qWords))
	}
	return 0
}

// Catalog returns the current catalog. Callers should not
// modify the returned slice.
func (f *Finder) Catalog() []Entry {
	return f.catalog
}

// CatalogSize returns the number of entries.
func (f *Finder) CatalogSize() int {
	return len(f.catalog)
}

// FormatResult renders the result as human-readable text
// suitable for injection into the model's tool response.
func FormatResult(r Result, library string) string {
	if !r.Found {
		return fmt.Sprintf("No known better alternative for %q. Proceed with the original choice.", library)
	}
	conf := fmt.Sprintf("%.0f%%", r.Confidence*100)
	return fmt.Sprintf(
		"Consider %s instead of %s: %s (confidence: %s)",
		r.Alternative, library, r.Reason, conf,
	)
}
