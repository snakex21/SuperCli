// Package freshness implements F18: staleness detection
// for the model catalog, skills, and learned patterns.
// Stale entries are flagged in --doctor output and
// patterns older than 90 days receive a confidence
// penalty so the injector ranks them lower.
package freshness

import (
	"fmt"
	"math"
	"time"
)

// Thresholds. Exported so tests and main.go can use
// the same constants.
const (
	CatalogStaleMonths = 18 // model not verified in 18 months
	SkillStaleDays     = 90 // skill file not modified in 90 days
	PatternStaleDays   = 90 // pattern not observed in 90 days
	PatternPenalty     = 0.3 // confidence multiplier for stale patterns
)

// PromptSection returns the freshness block injected into the
// system prompt: the current date plus an explicit warning that
// the model's training data may be stale. The agent loop puts
// this in front of the model on every run so it verifies
// current-state facts with tools instead of reconstructing them
// from memory.
func PromptSection(now time.Time) string {
	return "current_date: " + now.UTC().Format("2006-01-02") + "\n\n" +
		"Your training data may be stale. Library versions, model IDs, API shapes, " +
		"prices, and ecosystem facts may have changed since training. When current " +
		"state matters, verify via tools (web search, reading go.mod / package " +
		"manifests, running version commands) instead of reconstructing from memory. " +
		"Unfamiliar names, versions, or models released after your training cutoff " +
		"are expected and are not errors."
}

// CatalogEntry is a minimal read-only view of a model
// capability record. The freshness checker only needs
// LastVerified; everything else is opaque.
type CatalogEntry struct {
	ID           string
	LastVerified time.Time
}

// SkillEntry is a minimal view of a discovered skill.
type SkillEntry struct {
	Name     string
	Path     string
	Modified time.Time // file mtime of SKILL.md
}

// PatternEntry is a minimal view of a learned pattern.
type PatternEntry struct {
	ID         string
	Title      string
	Confidence float64
	CreatedAt  time.Time
	// MostRecent is the latest ObservedAt timestamp, or
	// CreatedAt if no observations are recorded.
	MostRecent time.Time
}

// StaleEntry describes one stale item for --doctor output.
type StaleEntry struct {
	Kind      string // "catalog", "skill", or "pattern"
	ID        string
	Detail    string // human-readable staleness info
	Age       time.Duration
	Threshold time.Duration
}

// Report is the aggregated staleness report for --doctor.
type Report struct {
	StaleModels  []StaleEntry
	StaleSkills  []StaleEntry
	StalePatterns []StaleEntry
}

// HasStale returns true if any staleness was detected.
func (r Report) HasStale() bool {
	return len(r.StaleModels) > 0 || len(r.StaleSkills) > 0 || len(r.StalePatterns) > 0
}

// Checker evaluates staleness for catalog entries, skills,
// and patterns. All methods are pure — no I/O, no state.
type Checker struct {
	NowFn func() time.Time // injectable for tests; nil = time.Now
}

// NewChecker returns a Checker using the real clock.
func NewChecker() *Checker {
	return &Checker{}
}

func (c *Checker) now() time.Time {
	if c.NowFn != nil {
		return c.NowFn()
	}
	return time.Now()
}

// CheckCatalog returns stale model entries — those whose
// LastVerified is older than CatalogStaleMonths.
func (c *Checker) CheckCatalog(entries []CatalogEntry) []StaleEntry {
	threshold := time.Duration(CatalogStaleMonths) * 30 * 24 * time.Hour // ~18 months
	now := c.now()
	var stale []StaleEntry
	for _, e := range entries {
		if e.LastVerified.IsZero() {
			continue // never probed — not stale, just unknown
		}
		age := now.Sub(e.LastVerified)
		if age > threshold {
			stale = append(stale, StaleEntry{
				Kind:      "catalog",
				ID:        e.ID,
				Detail:    fmt.Sprintf("last verified %s ago (threshold %d months)", age.Round(time.Hour*24), CatalogStaleMonths),
				Age:       age,
				Threshold: threshold,
			})
		}
	}
	return stale
}

// CheckSkills returns stale skill entries — those whose
// SKILL.md file was not modified in SkillStaleDays.
func (c *Checker) CheckSkills(entries []SkillEntry) []StaleEntry {
	threshold := time.Duration(SkillStaleDays) * 24 * time.Hour
	now := c.now()
	var stale []StaleEntry
	for _, e := range entries {
		if e.Modified.IsZero() {
			continue // can't determine age
		}
		age := now.Sub(e.Modified)
		if age > threshold {
			stale = append(stale, StaleEntry{
				Kind:      "skill",
				ID:        e.Name,
				Detail:    fmt.Sprintf("SKILL.md last modified %s ago (threshold %d days)", age.Round(time.Hour*24), SkillStaleDays),
				Age:       age,
				Threshold: threshold,
			})
		}
	}
	return stale
}

// CheckPatterns returns stale pattern entries and applies
// a confidence penalty. The returned patterns are copies
// with adjusted Confidence values — the originals are not
// mutated. Patterns older than PatternStaleDays receive a
// PatternPenalty multiplier (0.3 = 70% reduction).
func (c *Checker) CheckPatterns(entries []PatternEntry) ([]StaleEntry, []PatternEntry) {
	threshold := time.Duration(PatternStaleDays) * 24 * time.Hour
	now := c.now()
	var stale []StaleEntry
	var adjusted []PatternEntry
	for _, e := range entries {
		ref := e.MostRecent
		if ref.IsZero() {
			ref = e.CreatedAt
		}
		if ref.IsZero() {
			adjusted = append(adjusted, e)
			continue
		}
		age := now.Sub(ref)
		p := e // copy
		if age > threshold {
			stale = append(stale, StaleEntry{
				Kind:      "pattern",
				ID:        e.ID,
				Detail:    fmt.Sprintf("last observed %s ago (threshold %d days)", age.Round(time.Hour*24), PatternStaleDays),
				Age:       age,
				Threshold: threshold,
			})
			p.Confidence = math.Round(e.Confidence*PatternPenalty*100) / 100
		}
		adjusted = append(adjusted, p)
	}
	return stale, adjusted
}

// RunReport builds a full staleness report from the three
// data sources. Pass nil for any source to skip it.
func (c *Checker) RunReport(
	catalog []CatalogEntry,
	skills []SkillEntry,
	patterns []PatternEntry,
) Report {
	r := Report{}
	if catalog != nil {
		r.StaleModels = c.CheckCatalog(catalog)
	}
	if skills != nil {
		r.StaleSkills = c.CheckSkills(skills)
	}
	if patterns != nil {
		r.StalePatterns, _ = c.CheckPatterns(patterns)
	}
	return r
}

// FormatReport renders the report as human-readable text
// for --doctor output. Empty string when nothing is stale.
func FormatReport(r Report) string {
	if !r.HasStale() {
		return ""
	}
	var out string
	if len(r.StaleModels) > 0 {
		out += fmt.Sprintf("\nStale models (%d):\n", len(r.StaleModels))
		for _, s := range r.StaleModels {
			out += fmt.Sprintf("  [!!] %s: %s\n", s.ID, s.Detail)
		}
	}
	if len(r.StaleSkills) > 0 {
		out += fmt.Sprintf("\nStale skills (%d):\n", len(r.StaleSkills))
		for _, s := range r.StaleSkills {
			out += fmt.Sprintf("  [!!] %s: %s\n", s.ID, s.Detail)
		}
	}
	if len(r.StalePatterns) > 0 {
		out += fmt.Sprintf("\nStale patterns (%d, confidence penalized):\n", len(r.StalePatterns))
		for _, s := range r.StalePatterns {
			out += fmt.Sprintf("  [!!] %s: %s\n", s.ID, s.Detail)
		}
	}
	return out
}
