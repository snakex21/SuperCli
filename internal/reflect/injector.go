package reflect

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Injector reads patterns from a Store and renders the
// top-K most relevant ones as a system-prompt section.
// Build is called once at session start; the returned
// string is appended to the loop's Messages as a system
// message (F5.d).
//
// Relevance scoring: keyword overlap with the system
// context. A pattern's score is
//     |words(pattern) ∩ words(system)| / |words(pattern)|
// Patterns with score < MinScore are dropped. The top
// MaxPatterns are returned, ordered by score desc.
type Injector struct {
	// Store is required. The injector loads patterns
	// from the store at Build time.
	Store *Store
	// MaxPatterns caps the output. <=0 = default 3.
	MaxPatterns int
	// MinScore drops a pattern below this relevance
	// score. <=0 = default 0.2.
	MinScore float64
}

// Build returns the markdown section to inject. Empty
// string = no patterns worth showing. The section
// starts with a single "# " heading and lists each
// pattern as a "- " bullet.
func (i *Injector) Build(ctx context.Context, systemContext string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if i.Store == nil {
		return "", nil
	}
	if i.MaxPatterns <= 0 {
		i.MaxPatterns = 3
	}
	if i.MinScore <= 0 {
		i.MinScore = 0.2
	}

	// Load a generous superset; we will re-rank by
	// relevance and trim. Cap at 50 to keep the
	// scoring pass cheap.
	patterns, err := i.Store.List(ctx, 50)
	if err != nil {
		return "", fmt.Errorf("reflect: injector.Build: %w", err)
	}
	if len(patterns) == 0 {
		return "", nil
	}

	sysTokens := tokenize(systemContext)
	scored := make([]scoredPattern, 0, len(patterns))
	for _, p := range patterns {
		s := relevanceScore(p, sysTokens)
		if s < i.MinScore {
			continue
		}
		scored = append(scored, scoredPattern{p, s})
	}
	if len(scored) == 0 {
		return "", nil
	}
	sort.Slice(scored, func(a, b int) bool {
		if scored[a].score != scored[b].score {
			return scored[a].score > scored[b].score
		}
		return scored[a].p.Confidence > scored[b].p.Confidence
	})
	if len(scored) > i.MaxPatterns {
		scored = scored[:i.MaxPatterns]
	}

	return renderSection(scored), nil
}

type scoredPattern struct {
	p     Pattern
	score float64
}

// relevanceScore returns the keyword overlap between
// the pattern and the system context. The pattern's
// tokens are derived from its title, description, and
// tool. Empty system = everything scores 0 (we don't
// want to dump every pattern into a blank prompt).
func relevanceScore(p Pattern, sysTokens []string) float64 {
	if len(sysTokens) == 0 {
		return 0
	}
	pTokens := tokenize(p.Title + " " + p.Description + " " + p.Tool + " " + p.Reason)
	if len(pTokens) == 0 {
		return 0
	}
	hits := 0
	for _, t := range pTokens {
		for _, s := range sysTokens {
			if t == s {
				hits++
				break
			}
		}
	}
	return float64(hits) / float64(len(pTokens))
}

// renderSection produces a markdown block with a
// heading and bullet list, wrapped in a
// <system-reminder> envelope so the model treats it
// as background context rather than an instruction.
// Tries to stay compact — F5.d contributes to every
// system message, so we don't want to bloat it.
func renderSection(items []scoredPattern) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("## Relevant patterns learned from past sessions\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it.p.Title)
		if it.p.Description != "" {
			b.WriteString(": ")
			// Cap description at ~120 chars to keep
			// the section short.
			d := it.p.Description
			if len(d) > 120 {
				d = d[:117] + "..."
			}
			b.WriteString(d)
		}
		b.WriteString("\n")
	}
	b.WriteString("This context may or may not be relevant to the current task. " +
		"Only act on it if it is highly relevant; otherwise ignore it.\n")
	b.WriteString("</system-reminder>\n")
	return b.String()
}
