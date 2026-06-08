// Package context implements the context source algebra from the
// SuperCli plan. A Source represents a single, named, addressable
// piece of context (system prompt, file contents, tool output,
// previous turn, ...) that participates in the model's context window.
//
// F2.a extends the F0 Source struct with:
//   - Priority and TokenCap for budget enforcement
//   - Stale + LoadedAt for refresh tracking
//   - Meta any for opaque metadata
//   - Sources.FitToBudget(max) for compression
package context

import (
	"fmt"
	"strings"
	"time"
)

// Source is a named piece of context. Body is the raw string the
// model will eventually see; it may be markdown, JSON, plain text or
// anything else the source chooses.
type Source struct {
	Name string
	Body string

	// Priority is the importance of this source relative to
	// others. Higher = kept first when FitToBudget must drop
	// sources to fit a token limit. The system prompt is
	// typically 100, tool errors 95, user content 80, etc.
	Priority int

	// Stale is set by a loader when the underlying data has
	// changed since LoadedAt. The Builder or Loop will
	// re-render the source on the next pass.
	Stale bool

	// LoadedAt is when Body was last refreshed. Sources with
	// zero LoadedAt are considered uninitialised.
	LoadedAt time.Time

	// TokenCap is the maximum number of tokens the source
	// may contribute. Zero means no cap. When over the cap,
	// the source itself is responsible for truncating or
	// summarising; FitToBudget only removes whole sources.
	TokenCap int

	// Meta is opaque metadata. Loaders may attach a path,
	// timestamp, or struct value that other code can inspect
	// without changing the wire format.
	Meta any
}

// NewSource builds a Source. Empty name is rejected because the name
// is what shows up in logs and in the LLM's own understanding of
// "where did this come from".
func NewSource(name, body string) (Source, error) {
	if name == "" {
		return Source{}, fmt.Errorf("context.NewSource: name is empty")
	}
	return Source{Name: name, Body: body, LoadedAt: time.Now().UTC()}, nil
}

// MustSource is a convenience for tests and known-good literals. It
// panics on invalid input; do not use it on user-controlled data.
func MustSource(name, body string) Source {
	s, err := NewSource(name, body)
	if err != nil {
		panic(err)
	}
	return s
}

// EstimateTokens returns a rough character/4 estimate of the source's
// contribution to the model context. The factor of 4 is the common
// rule of thumb for English text in tokenizers used by GPT-4-class
// models; for code-heavy bodies it is conservative (i.e. slightly
// high). The real tokenizer lands in F2.g.
func (s Source) EstimateTokens() int {
	n := len(s.Body)
	return (n + 3) / 4 // ceiling division
}

// Sources is an ordered, name-unique collection. Order matters
// because the model sees sources in the order they appear in the
// system prompt, and certain sources (e.g. the "primary instruction")
// must always be first.
type Sources struct {
	items []Source
}

// NewSources returns an empty Sources.
func NewSources() Sources { return Sources{} }

// Append adds a source. If a source with the same name already
// exists, the older one is replaced and its previous position is
// kept. This matches the "update in place" semantic from the plan.
func (s *Sources) Append(src Source) {
	for i, existing := range s.items {
		if existing.Name == src.Name {
			s.items[i] = src
			return
		}
	}
	s.items = append(s.items, src)
}

// Replace is like Append but the new source goes to the same index
// even if absent. Used by refresh logic that wants to preserve
// declared order.
func (s *Sources) Replace(src Source) error {
	if src.Name == "" {
		return fmt.Errorf("context.Sources.Replace: name is empty")
	}
	for i, existing := range s.items {
		if existing.Name == src.Name {
			s.items[i] = src
			return nil
		}
	}
	s.items = append(s.items, src)
	return nil
}

// Len reports how many sources are stored.
func (s Sources) Len() int { return len(s.items) }

// TotalTokens sums EstimateTokens across all sources.
func (s Sources) TotalTokens() int {
	total := 0
	for _, it := range s.items {
		total += it.EstimateTokens()
	}
	return total
}

// Names returns the source names in order.
func (s Sources) Names() []string {
	out := make([]string, len(s.items))
	for i, it := range s.items {
		out[i] = it.Name
	}
	return out
}

// Get returns a copy of the source by name and a found flag.
func (s Sources) Get(name string) (Source, bool) {
	for _, it := range s.items {
		if it.Name == name {
			return it, true
		}
	}
	return Source{}, false
}

// MarkStale sets Stale=true on the source by name. Loaders use this
// to flag that a re-render is needed.
func (s *Sources) MarkStale(name string) {
	for i, it := range s.items {
		if it.Name == name {
			s.items[i].Stale = true
			return
		}
	}
}

// StaleNames returns the names of sources marked Stale.
func (s Sources) StaleNames() []string {
	var out []string
	for _, it := range s.items {
		if it.Stale {
			out = append(out, it.Name)
		}
	}
	return out
}

// FitToBudget returns a new Sources where the total EstimateTokens
// is at or under max. Sources are dropped in order of ascending
// Priority; ties are broken by reverse insertion order (so
// recently appended ones survive). The original collection is
// unchanged.
//
// This is a coarse strategy: it removes whole sources, never
// truncates an individual source. Per-source truncation is the
// responsibility of the source's own loader.
func (s Sources) FitToBudget(max int) Sources {
	if max <= 0 || s.TotalTokens() <= max {
		// Return a copy so callers can mutate freely.
		cp := s
		cp.items = append([]Source(nil), s.items...)
		return cp
	}
	// Sort indices by priority ascending; on tie, by original
	// index descending (later-inserted wins). The lowest-priority
	// source is at index 0 of the "to drop" slice.
	order := make([]dropIdx, len(s.items))
	for i, it := range s.items {
		order[i] = dropIdx{i, it.Priority}
	}
	// Sort: drop order = ascending priority, descending insertion.
	// We use a stable-ish insertion sort to keep semantics clear.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0; j-- {
			if shouldDropBeforeIdx(order[j], order[j-1]) {
				order[j], order[j-1] = order[j-1], order[j]
			} else {
				break
			}
		}
	}
	// Now drop from the front until total <= max.
	total := s.TotalTokens()
	drop := make(map[int]bool)
	for _, x := range order {
		if total <= max {
			break
		}
		drop[x.i] = true
		total -= s.items[x.i].EstimateTokens()
	}
	kept := make([]Source, 0, len(s.items)-len(drop))
	for i, it := range s.items {
		if !drop[i] {
			kept = append(kept, it)
		}
	}
	return Sources{items: kept}
}

// shouldDropBeforeIdx returns true if x should be dropped before y:
// x has strictly lower priority, or equal priority but x was
// inserted later (higher index).
func shouldDropBeforeIdx(x, y dropIdx) bool {
	if x.priority != y.priority {
		return x.priority < y.priority
	}
	return x.i > y.i
}

type dropIdx struct {
	i        int
	priority int
}

// Render concatenates all sources in declared order, separated by
// blank lines, prefixed with `## <name>` headers. The output is
// suitable for a single system message.
//
// If max > 0 and the raw render would exceed max, FitToBudget is
// applied first. The returned count is the actual token total
// of the rendered string.
func (s Sources) Render(max int) (string, int) {
	srcs := s
	if max > 0 && srcs.TotalTokens() > max {
		srcs = s.FitToBudget(max)
	}
	var b strings.Builder
	for _, it := range srcs.items {
		if it.Body == "" {
			continue
		}
		b.WriteString("## ")
		b.WriteString(it.Name)
		b.WriteString("\n")
		b.WriteString(it.Body)
		b.WriteString("\n\n")
	}
	out := b.String()
	return out, (len(out) + 3) / 4
}
